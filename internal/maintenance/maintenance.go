// Package maintenance deletes what handbill no longer needs: expired
// sessions, tickets, flows, and OAuth rows; stale invites and transfers;
// abandoned deployments; and blobs no version or open deployment uses
// (docs/design.md#operations). One goroutine per replica wakes every
// MAINTENANCE_INTERVAL; a PostgreSQL advisory lock lets only one replica
// work at a time.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/metrics"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/storage"
)

// Retention after a row stops being usable.
const (
	// ExpiredGrace keeps expired single-use rows (sessions, tickets,
	// flows, verification links, OAuth requests, codes, and tokens) a
	// little past their expiry, so a request racing the expiry is answered
	// "expired" by its own handler rather than by a missing row.
	ExpiredGrace = time.Hour
	// UnusedClientAge is how long an OAuth client nobody approved is kept.
	UnusedClientAge = 24 * time.Hour
	// InviteRetention keeps expired and revoked invites and transfers, so
	// their links say "expired" rather than "not found" for a while.
	// Accepted ones are kept with their site as the record of the grant.
	InviteRetention = 30 * 24 * time.Hour
	// OpenDeploymentAge abandons staged deployments never finalized.
	OpenDeploymentAge = 24 * time.Hour
	// AbandonedRetention deletes abandoned deployments (and their file
	// lists), leaving their blobs to blob GC.
	AbandonedRetention = 7 * 24 * time.Hour
)

// lockID is the advisory lock held while a replica runs maintenance
// (distinct from goose's migration lock).
const lockID int64 = 0x68616e6462696c6c // "handbill"

// batch bounds the blobs or prefixes handled per query.
const batch = 500

// Runner runs the maintenance jobs.
type Runner struct {
	db       *pgxpool.Pool
	store    storage.Store
	log      *slog.Logger
	interval time.Duration
	blobGC   time.Duration
	now      func() time.Time
}

// New returns a runner over d with the configured interval and blob grace.
func New(d *server.Deps) *Runner {
	return &Runner{
		db: d.DB, store: d.Storage, log: d.Logger,
		interval: d.Config.Operations.MaintenanceInterval,
		blobGC:   d.Config.Operations.BlobGCGrace,
		now:      time.Now,
	}
}

// Run runs maintenance at once and then every interval until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		switch ran, err := r.RunOnce(ctx); {
		case ctx.Err() != nil:
			return
		case err != nil:
			metrics.MaintenanceRuns.WithLabelValues("error").Inc()
			r.log.Error("maintenance failed", "err", err)
		case !ran:
			metrics.MaintenanceRuns.WithLabelValues("skipped").Inc()
		default:
			metrics.MaintenanceRuns.WithLabelValues("ok").Inc()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce runs every job once if no other replica is; ran reports whether
// it did. Jobs continue past a failing job; the errors are joined.
func (r *Runner) RunOnce(ctx context.Context) (ran bool, err error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("maintenance: acquire: %w", err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&locked); err != nil {
		return false, fmt.Errorf("maintenance: lock: %w", err)
	}
	if !locked {
		return false, nil
	}
	defer func() {
		if _, uerr := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID); uerr != nil {
			// The lock goes with the connection; drop it rather than return
			// it to the pool still holding the lock.
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
		}
	}()

	start := r.now()
	var errs []error
	for _, j := range r.jobs() {
		n, err := j.run(ctx)
		if n > 0 {
			metrics.MaintenanceDeleted.WithLabelValues(j.name).Add(float64(n))
			r.log.Info("maintenance", "job", j.name, "count", n)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", j.name, err))
		}
		if ctx.Err() != nil {
			break
		}
	}
	r.log.Debug("maintenance done", "duration_ms", r.now().Sub(start).Milliseconds())
	return true, errors.Join(errs...)
}

type job struct {
	name string
	run  func(context.Context) (int64, error)
}

func (r *Runner) jobs() []job {
	q := db.New(r.db)
	before := func(d time.Duration) time.Time { return r.now().Add(-d) }
	rows := func(name string, del func(context.Context, time.Time) (int64, error), age time.Duration) job {
		return job{name, func(ctx context.Context) (int64, error) { return del(ctx, before(age)) }}
	}
	return []job{
		rows("sessions", q.DeleteStaleSessions, ExpiredGrace),
		rows("site_tickets", q.DeleteStaleSiteTickets, ExpiredGrace),
		rows("auth_flows", q.DeleteStaleAuthFlows, ExpiredGrace),
		rows("email_verifications", q.DeleteStaleEmailVerifications, ExpiredGrace),
		rows("oauth_requests", q.DeleteStaleAuthorizationRequests, ExpiredGrace),
		rows("oauth_codes", q.DeleteStaleAuthorizationCodes, ExpiredGrace),
		rows("oauth_access_tokens", q.DeleteStaleAccessTokens, ExpiredGrace),
		rows("oauth_refresh_tokens", q.DeleteStaleRefreshTokens, ExpiredGrace),
		// After requests: a client is unused once its pending requests go.
		rows("oauth_clients", q.DeleteUnusedOAuthClients, UnusedClientAge),
		rows("invites", q.DeleteStaleInvites, InviteRetention),
		rows("ownership_transfers", q.DeleteStaleOwnershipTransfers, InviteRetention),
		rows("deployments_abandoned", q.AbandonStaleDeployments, OpenDeploymentAge),
		rows("deployments_deleted", q.DeleteAbandonedDeployments, AbandonedRetention),
		{"blob_gc", r.blobGCJob},
		{"storage_prefixes", r.storagePrefixes},
	}
}

// blobGCJob deletes blobs no version or open deployment of their site uses
// and that were last staged more than BLOB_GC_GRACE ago. Each row is
// deleted in a transaction that commits only after the object is gone, so
// a concurrent upload of the same blob either refreshed it first (and GC
// skips it) or waits and stores it again.
func (r *Runner) blobGCJob(ctx context.Context) (int64, error) {
	var total int64
	for {
		cutoff := r.now().Add(-r.blobGC)
		cands, err := db.New(r.db).ListGCBlobs(ctx, db.ListGCBlobsParams{Before: cutoff, MaxRows: batch})
		if err != nil {
			return total, err
		}
		for _, c := range cands {
			deleted := false
			err := db.InTx(ctx, r.db, func(q *db.Queries) error {
				n, err := q.DeleteGCBlob(ctx, db.DeleteGCBlobParams{SiteID: c.SiteID, Sha256: c.Sha256, Before: cutoff})
				if err != nil || n == 0 {
					return err
				}
				if err := r.store.Delete(ctx, sites.BlobKey(c.SiteID, c.Sha256)); err != nil {
					return fmt.Errorf("delete object: %w", err)
				}
				deleted = true
				return nil
			})
			if err != nil {
				return total, err
			}
			if deleted {
				total++
			}
		}
		if len(cands) < batch {
			return total, nil
		}
	}
}

// storagePrefixes retries deleting the storage of deleted sites.
func (r *Runner) storagePrefixes(ctx context.Context) (int64, error) {
	q := db.New(r.db)
	prefixes, err := q.ListStorageDeletions(ctx, batch)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, p := range prefixes {
		if err := r.store.DeletePrefix(ctx, p); err != nil {
			return n, fmt.Errorf("delete %s: %w", p, err)
		}
		if err := q.DeleteStorageDeletion(ctx, p); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
