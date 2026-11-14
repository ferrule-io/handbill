package db_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/db/dbtest"
	"github.com/ferrule-io/handbill/internal/siteid"
	"github.com/ferrule-io/handbill/internal/token"
)

// PostgreSQL error codes the schema relies on.
const (
	foreignKeyViolation = "23503"
	uniqueViolation     = "23505"
	checkViolation      = "23514"
	restrictViolation   = "23001"
)

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func wantCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	if got := pgCode(err); got != code {
		t.Fatalf("%s: want SQLSTATE %s, got %v", what, code, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// inTx runs raw statements in one transaction and returns the first error,
// including a commit-time (deferred constraint) error.
func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func newUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	u, err := db.New(pool).CreateUser(t.Context(), newID())
	must(t, err)
	return u.ID
}

// newSite creates a site owned by owner the way every caller must: site and
// owner membership in one transaction.
func newSite(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) string {
	t.Helper()
	id := siteid.New()
	must(t, db.InTx(t.Context(), pool, func(q *db.Queries) error {
		if _, err := q.CreateSite(t.Context(), db.CreateSiteParams{ID: id, Title: "t", OwnerID: owner}); err != nil {
			return err
		}
		return q.CreateMembership(t.Context(), db.CreateMembershipParams{SiteID: id, UserID: owner, Role: "owner"})
	}))
	return id
}

func sha(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

// schemaFingerprint describes every column, constraint, index, and function
// in the public schema.
func schemaFingerprint(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var s string
	err := pool.QueryRow(t.Context(), `
SELECT coalesce(string_agg(x, E'\n' ORDER BY x), '') FROM (
  SELECT format('col %s.%s %s %s %s', table_name, column_name, data_type, is_nullable, column_default) AS x
    FROM information_schema.columns WHERE table_schema = 'public' AND table_name <> 'goose_db_version'
  UNION ALL
  SELECT format('con %s %s %s', conrelid::regclass, conname, pg_get_constraintdef(oid))
    FROM pg_constraint WHERE connamespace = 'public'::regnamespace AND conrelid <> 'goose_db_version'::regclass
  UNION ALL
  SELECT format('idx %s', indexdef) FROM pg_indexes WHERE schemaname = 'public' AND tablename <> 'goose_db_version'
  UNION ALL
  SELECT format('fn %s', proname) FROM pg_proc WHERE pronamespace = 'public'::regnamespace
  UNION ALL
  SELECT format('trg %s %s', tgrelid::regclass, tgname) FROM pg_trigger t
    JOIN pg_class c ON c.oid = t.tgrelid WHERE c.relnamespace = 'public'::regnamespace AND NOT tgisinternal
) s`).Scan(&s)
	must(t, err)
	return s
}

func TestMigrationsRoundTrip(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()
	up := schemaFingerprint(t, pool)
	for _, table := range []string{"users", "sites", "memberships", "oauth_refresh_tokens"} {
		if !strings.Contains(up, "col "+table+".") {
			t.Fatalf("table %s missing after migrating", table)
		}
	}

	p, err := db.NewProvider(pool)
	must(t, err)
	defer p.Close()
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if down := schemaFingerprint(t, pool); down != "" {
		t.Fatalf("schema left after rolling back:\n%s", down)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != 0 {
		t.Fatalf("version after down = %d, %v", v, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if again := schemaFingerprint(t, pool); again != up {
		t.Fatalf("schema differs after down+up:\n--- first\n%s\n--- again\n%s", up, again)
	}
	if pending, err := p.HasPending(ctx); err != nil || pending {
		t.Fatalf("pending after up = %v, %v", pending, err)
	}
}

func TestInTxRollsBack(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	sentinel := errors.New("stop")
	u := newID()
	err := db.InTx(t.Context(), pool, func(q *db.Queries) error {
		if _, err := q.CreateUser(t.Context(), u); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx err = %v, want sentinel", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE id = $1", u); n != 0 {
		t.Fatal("InTx committed despite an error")
	}
}

func TestSiteIDCheck(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	owner := newUser(t, pool)
	for _, id := range []string{"", "abcdefghijk", "abcdefghijklm", "ABCDEFGHIJKL", "abcdefgh2341", "abcdefgh234-", "abcdefgh234 "} {
		err := db.InTx(t.Context(), pool, func(q *db.Queries) error {
			_, err := q.CreateSite(t.Context(), db.CreateSiteParams{ID: id, Title: "t", OwnerID: owner})
			return err
		})
		wantCode(t, "site id "+id, err, checkViolation)
	}
	id := newSite(t, pool, owner)
	if s, err := db.New(pool).GetSite(t.Context(), id); err != nil || s.OwnerID != owner || s.ID != id {
		t.Fatalf("GetSite = %+v, %v", s, err)
	}
}

func TestExactlyOneOwner(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()
	q := db.New(pool)
	a, b := newUser(t, pool), newUser(t, pool)

	// A site without its owner membership does not commit.
	err := db.InTx(ctx, pool, func(q *db.Queries) error {
		_, err := q.CreateSite(ctx, db.CreateSiteParams{ID: siteid.New(), Title: "t", OwnerID: a})
		return err
	})
	wantCode(t, "site without owner membership", err, foreignKeyViolation)

	// The owner membership must be the site's owner_id.
	err = db.InTx(ctx, pool, func(q *db.Queries) error {
		id := siteid.New()
		if _, err := q.CreateSite(ctx, db.CreateSiteParams{ID: id, Title: "t", OwnerID: a}); err != nil {
			return err
		}
		return q.CreateMembership(ctx, db.CreateMembershipParams{SiteID: id, UserID: b, Role: "owner"})
	})
	wantCode(t, "owner membership for another user", err, foreignKeyViolation)

	site := newSite(t, pool, a)

	// No second owner.
	err = q.CreateMembership(ctx, db.CreateMembershipParams{SiteID: site, UserID: b, Role: "owner"})
	wantCode(t, "second owner", err, uniqueViolation)
	must(t, q.CreateMembership(ctx, db.CreateMembershipParams{SiteID: site, UserID: b, Role: "viewer"}))
	_, err = pool.Exec(ctx, "UPDATE memberships SET role = 'owner' WHERE site_id = $1 AND user_id = $2", site, b)
	wantCode(t, "promote a second owner", err, uniqueViolation)

	// Unknown roles are rejected.
	_, err = pool.Exec(ctx, "UPDATE memberships SET role = 'admin' WHERE site_id = $1 AND user_id = $2", site, b)
	wantCode(t, "unknown role", err, checkViolation)

	// Removing or demoting the owner without a replacement does not commit.
	_, err = pool.Exec(ctx, "DELETE FROM memberships WHERE site_id = $1 AND user_id = $2", site, a)
	wantCode(t, "delete owner membership", err, foreignKeyViolation)
	_, err = pool.Exec(ctx, "UPDATE memberships SET role = 'manager' WHERE site_id = $1 AND user_id = $2", site, a)
	wantCode(t, "demote owner", err, foreignKeyViolation)

	// Pointing owner_id at a non-owner member does not commit.
	_, err = pool.Exec(ctx, "UPDATE sites SET owner_id = $2 WHERE id = $1", site, b)
	wantCode(t, "owner_id at a viewer", err, foreignKeyViolation)

	// A transfer (demote, promote, repoint in one transaction) commits.
	must(t, inTx(ctx, pool, func(tx pgx.Tx) error {
		const setRole = "UPDATE memberships SET role = $3 WHERE site_id = $1 AND user_id = $2"
		if _, err := tx.Exec(ctx, setRole, site, a, "manager"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, setRole, site, b, "owner"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE sites SET owner_id = $2 WHERE id = $1", site, b)
		return err
	}))
	s, err := q.GetSite(ctx, site)
	must(t, err)
	if s.OwnerID != b {
		t.Fatalf("owner after transfer = %v, want %v", s.OwnerID, b)
	}
	if n := count(t, pool, "SELECT count(*) FROM memberships WHERE site_id = $1 AND role = 'owner' AND user_id = $2", site, b); n != 1 {
		t.Fatal("new owner membership missing")
	}
}

func TestSingleUseConsumption(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()
	q := db.New(pool)
	user := newUser(t, pool)
	site := newSite(t, pool, user)
	future, past := time.Now().Add(time.Minute), time.Now().Add(-time.Second)

	t.Run("site ticket", func(t *testing.T) {
		plain, hash := token.New()
		_, err := q.CreateSiteTicket(ctx, db.CreateSiteTicketParams{ID: newID(), TokenHash: hash, SiteID: site, UserID: user, ReturnPath: "/a?b", ExpiresAt: future})
		must(t, err)
		got, err := q.ConsumeSiteTicket(ctx, token.Hash(plain))
		must(t, err)
		if got.SiteID != site || got.UserID != user || got.ReturnPath != "/a?b" {
			t.Fatalf("consumed %+v", got)
		}
		if _, err := q.ConsumeSiteTicket(ctx, hash); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("second use: err = %v, want ErrNoRows", err)
		}

		_, expired := token.New()
		_, err = q.CreateSiteTicket(ctx, db.CreateSiteTicketParams{ID: newID(), TokenHash: expired, SiteID: site, UserID: user, ReturnPath: "/", ExpiresAt: past})
		must(t, err)
		if _, err := q.ConsumeSiteTicket(ctx, expired); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expired: err = %v, want ErrNoRows", err)
		}
		if _, err := q.ConsumeSiteTicket(ctx, sha("unknown")); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("unknown: err = %v, want ErrNoRows", err)
		}
		_, err = q.CreateSiteTicket(ctx, db.CreateSiteTicketParams{ID: newID(), TokenHash: hash, SiteID: site, UserID: user, ReturnPath: "/", ExpiresAt: future})
		wantCode(t, "duplicate token hash", err, uniqueViolation)
		_, err = q.CreateSiteTicket(ctx, db.CreateSiteTicketParams{ID: newID(), TokenHash: []byte("short"), SiteID: site, UserID: user, ReturnPath: "/", ExpiresAt: future})
		wantCode(t, "non-SHA-256 token hash", err, checkViolation)
	})

	t.Run("invite", func(t *testing.T) {
		invitee := newUser(t, pool)
		create := func(expires time.Time) []byte {
			_, hash := token.New()
			_, err := q.CreateInvite(ctx, db.CreateInviteParams{ID: newID(), TokenHash: hash, SiteID: site, Email: "friend@example.com", Role: "viewer", InvitedBy: uuid.NullUUID{UUID: user, Valid: true}, ExpiresAt: expires})
			must(t, err)
			return hash
		}

		// Concurrent acceptances: exactly one wins.
		hash := create(future)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for range 8 {
			wg.Go(func() {
				_, err := q.ConsumeInvite(ctx, db.ConsumeInviteParams{TokenHash: hash, UserID: uuid.NullUUID{UUID: invitee, Valid: true}})
				if err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				} else if !errors.Is(err, pgx.ErrNoRows) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("%d concurrent acceptances succeeded, want 1", wins)
		}
		if n := count(t, pool, "SELECT count(*) FROM invites WHERE token_hash = $1 AND consumed_by = $2", hash, invitee); n != 1 {
			t.Fatal("consumed_by not recorded")
		}

		expired := create(past)
		if _, err := q.ConsumeInvite(ctx, db.ConsumeInviteParams{TokenHash: expired}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expired: err = %v, want ErrNoRows", err)
		}
		revoked := create(future)
		exec(t, pool, "UPDATE invites SET revoked_at = now() WHERE token_hash = $1", revoked)
		if _, err := q.ConsumeInvite(ctx, db.ConsumeInviteParams{TokenHash: revoked}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("revoked: err = %v, want ErrNoRows", err)
		}
		_, err := q.CreateInvite(ctx, db.CreateInviteParams{ID: newID(), TokenHash: sha("owner"), SiteID: site, Email: "x@example.com", Role: "owner", ExpiresAt: future})
		wantCode(t, "invite as owner", err, checkViolation)
	})

	t.Run("email verification", func(t *testing.T) {
		e, err := q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: user, Email: "me@example.com", IsPrimary: true})
		must(t, err)
		plain, hash := token.New()
		_, err = q.CreateEmailVerification(ctx, db.CreateEmailVerificationParams{ID: newID(), TokenHash: hash, EmailID: e.ID, ExpiresAt: future})
		must(t, err)
		got, err := q.ConsumeEmailVerification(ctx, token.Hash(plain))
		if err != nil || got != e.ID {
			t.Fatalf("consume = %v, %v", got, err)
		}
		if _, err := q.ConsumeEmailVerification(ctx, hash); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("second use: err = %v, want ErrNoRows", err)
		}
		_, expired := token.New()
		_, err = q.CreateEmailVerification(ctx, db.CreateEmailVerificationParams{ID: newID(), TokenHash: expired, EmailID: e.ID, ExpiresAt: past})
		must(t, err)
		if _, err := q.ConsumeEmailVerification(ctx, expired); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expired: err = %v, want ErrNoRows", err)
		}
	})
}

func TestVerifiedEmailUniqueness(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()
	q := db.New(pool)
	a, b, c := newUser(t, pool), newUser(t, pool), newUser(t, pool)
	const addr = "shared@example.com"

	// Any number of accounts may claim an address unverified.
	ea, err := q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: a, Email: addr, IsPrimary: true})
	must(t, err)
	eb, err := q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: b, Email: addr, IsPrimary: true})
	must(t, err)
	_, err = q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: c, Email: addr})
	must(t, err)

	// Only one may hold it verified.
	_, err = q.MarkEmailVerified(ctx, ea.ID)
	must(t, err)
	_, err = q.MarkEmailVerified(ctx, eb.ID)
	wantCode(t, "second verified holder", err, uniqueViolation)
	_, err = pool.Exec(ctx, "UPDATE emails SET verified = true WHERE id = $1", eb.ID)
	wantCode(t, "verified without verified_at", err, checkViolation)

	// Verifying displaces other accounts' unverified claims, not the holder's.
	n, err := q.DisplaceUnverifiedEmailClaims(ctx, db.DisplaceUnverifiedEmailClaimsParams{Email: addr, UserID: a})
	must(t, err)
	if n != 2 {
		t.Fatalf("displaced %d claims, want 2", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM emails WHERE email = $1", addr); n != 1 {
		t.Fatalf("%d claims left, want 1", n)
	}

	_, err = q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: a, Email: addr})
	wantCode(t, "same address twice for one user", err, uniqueViolation)
	_, err = q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: a, Email: "other@example.com", IsPrimary: true})
	wantCode(t, "second primary address", err, uniqueViolation)
	_, err = q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: b, Email: "Mixed@Example.com"})
	wantCode(t, "non-lowercased address", err, checkViolation)
}

// populateSite adds one row of every site-owned kind to site.
func populateSite(t *testing.T, pool *pgxpool.Pool, site string, owner, viewer uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	q := db.New(pool)
	blob := sha(site)
	must(t, q.CreateMembership(ctx, db.CreateMembershipParams{SiteID: site, UserID: viewer, Role: "viewer"}))
	v, err := q.CreateVersion(ctx, db.CreateVersionParams{ID: newID(), SiteID: site, PublishedBy: uuid.NullUUID{UUID: owner, Valid: true}})
	must(t, err)
	must(t, q.CreateVersionFile(ctx, db.CreateVersionFileParams{VersionID: v.ID, Path: "index.html", BlobSha256: blob, Size: 5, ContentType: "text/html"}))
	if n, err := q.SetCurrentVersion(ctx, db.SetCurrentVersionParams{ID: site, VersionID: uuid.NullUUID{UUID: v.ID, Valid: true}}); err != nil || n != 1 {
		t.Fatalf("SetCurrentVersion = %d, %v", n, err)
	}
	exec(t, pool, "INSERT INTO site_blobs (site_id, sha256, size) VALUES ($1, $2, 5)", site, blob)
	dep := newID()
	exec(t, pool, "INSERT INTO deployments (id, site_id, created_by, mode) VALUES ($1, $2, $3, 'merge')", dep, site, owner)
	exec(t, pool, "INSERT INTO deployment_files (deployment_id, path, blob_sha256, size, content_type) VALUES ($1, 'a.css', $2, 5, 'text/css')", dep, blob)
	exec(t, pool, "INSERT INTO deployment_files (deployment_id, path, deleted) VALUES ($1, 'old.html', true)", dep)
	_, err = q.CreateInvite(ctx, db.CreateInviteParams{ID: newID(), TokenHash: sha("invite" + site), SiteID: site, Email: "i@example.com", Role: "manager", ExpiresAt: time.Now().Add(time.Hour)})
	must(t, err)
	exec(t, pool, "INSERT INTO ownership_transfers (id, token_hash, site_id, email, from_user_id, expires_at) VALUES ($1, $2, $3, 't@example.com', $4, now() + interval '1 hour')", newID(), sha("transfer"+site), site, owner)
	exec(t, pool, "INSERT INTO public_links (site_id, token_hash) VALUES ($1, $2)", site, sha("link"+site))
	_, err = q.CreateSiteTicket(ctx, db.CreateSiteTicketParams{ID: newID(), TokenHash: sha("ticket" + site), SiteID: site, UserID: viewer, ReturnPath: "/", ExpiresAt: time.Now().Add(time.Minute)})
	must(t, err)
}

var siteTables = map[string]string{
	"sites":               "SELECT count(*) FROM sites",
	"memberships":         "SELECT count(*) FROM memberships",
	"versions":            "SELECT count(*) FROM versions",
	"version_files":       "SELECT count(*) FROM version_files",
	"site_blobs":          "SELECT count(*) FROM site_blobs",
	"deployments":         "SELECT count(*) FROM deployments",
	"deployment_files":    "SELECT count(*) FROM deployment_files",
	"invites":             "SELECT count(*) FROM invites",
	"ownership_transfers": "SELECT count(*) FROM ownership_transfers",
	"public_links":        "SELECT count(*) FROM public_links",
	"site_tickets":        "SELECT count(*) FROM site_tickets",
}

func TestSiteDeletionCascades(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	owner, viewer := newUser(t, pool), newUser(t, pool)
	doomed, kept := newSite(t, pool, owner), newSite(t, pool, owner)
	populateSite(t, pool, doomed, owner, viewer)
	populateSite(t, pool, kept, owner, viewer)

	before := map[string]int{}
	for table, sql := range siteTables {
		before[table] = count(t, pool, sql)
		if before[table] == 0 || before[table]%2 != 0 {
			t.Fatalf("%s has %d rows before deletion, want an even nonzero count", table, before[table])
		}
	}
	n, err := db.New(pool).DeleteSite(t.Context(), doomed)
	if err != nil || n != 1 {
		t.Fatalf("DeleteSite = %d, %v", n, err)
	}
	for table, sql := range siteTables {
		if got := count(t, pool, sql); got != before[table]/2 {
			t.Errorf("%s: %d rows after deleting one of two sites, want %d", table, got, before[table]/2)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM users"); n != 2 {
		t.Fatalf("users after site deletion = %d, want 2", n)
	}
}

func TestUserDeletion(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()
	q := db.New(pool)
	owner, member := newUser(t, pool), newUser(t, pool)
	site := newSite(t, pool, owner)
	must(t, q.CreateMembership(ctx, db.CreateMembershipParams{SiteID: site, UserID: member, Role: "manager"}))
	v, err := q.CreateVersion(ctx, db.CreateVersionParams{ID: newID(), SiteID: site, PublishedBy: uuid.NullUUID{UUID: member, Valid: true}})
	must(t, err)
	exec(t, pool, "INSERT INTO sessions (id, token_hash, user_id, expires_at) VALUES ($1, $2, $3, now() + interval '1 day')", newID(), sha("session"), member)
	exec(t, pool, "INSERT INTO recovery_codes (id, user_id, code_hash, lookup_hash) VALUES ($1, $2, '$argon2id$v=19$x', $3)", newID(), member, sha("recovery"))
	exec(t, pool, "INSERT INTO passkey_credentials (id, user_id, credential_id, public_key) VALUES ($1, $2, '\\x01', '\\x02')", newID(), member)
	exec(t, pool, "INSERT INTO google_identities (id, user_id, sub) VALUES ($1, $2, 'sub-1')", newID(), member)

	// An owner cannot be deleted while they own a site. (PostgreSQL 17
	// reports ON DELETE RESTRICT as 23503, 18 as 23001.)
	_, err = q.DeleteUser(ctx, owner)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.ConstraintName != "sites_owner_id_fkey" {
		t.Fatalf("delete site owner: want sites_owner_id_fkey violation, got %v", err)
	}

	n, err := q.DeleteUser(ctx, member)
	if err != nil || n != 1 {
		t.Fatalf("DeleteUser(member) = %d, %v", n, err)
	}
	for _, table := range []string{"memberships", "sessions", "recovery_codes", "passkey_credentials", "google_identities"} {
		if n := count(t, pool, "SELECT count(*) FROM "+table+" WHERE user_id = $1", member); n != 0 {
			t.Errorf("%s rows left for deleted user: %d", table, n)
		}
	}
	var by uuid.NullUUID
	must(t, pool.QueryRow(ctx, "SELECT published_by FROM versions WHERE id = $1", v.ID).Scan(&by))
	if by.Valid {
		t.Fatal("version keeps the deleted publisher")
	}

	// Once the site is gone, the owner can be deleted.
	_, err = q.DeleteSite(ctx, site)
	must(t, err)
	_, err = q.DeleteUser(ctx, owner)
	must(t, err)
}

func TestVersionsImmutable(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()
	q := db.New(pool)
	owner := newUser(t, pool)
	site, other := newSite(t, pool, owner), newSite(t, pool, owner)
	v, err := q.CreateVersion(ctx, db.CreateVersionParams{ID: newID(), SiteID: site})
	must(t, err)
	must(t, q.CreateVersionFile(ctx, db.CreateVersionFileParams{VersionID: v.ID, Path: "index.html", BlobSha256: sha("x"), Size: 1, ContentType: "text/html"}))

	for _, s := range []string{
		"UPDATE version_files SET size = 2 WHERE version_id = $1",
		"UPDATE versions SET created_at = now() - interval '1 day' WHERE id = $1",
		"UPDATE versions SET published_by = '" + owner.String() + "' WHERE id = $1",
	} {
		_, err := pool.Exec(ctx, s, v.ID)
		wantCode(t, s, err, restrictViolation)
	}
	err = q.CreateVersionFile(ctx, db.CreateVersionFileParams{VersionID: v.ID, Path: "/abs.html", BlobSha256: sha("x"), Size: 1, ContentType: "text/html"})
	wantCode(t, "absolute file path", err, checkViolation)

	// A site's current version must be one of its own.
	_, err = q.SetCurrentVersion(ctx, db.SetCurrentVersionParams{ID: other, VersionID: uuid.NullUUID{UUID: v.ID, Valid: true}})
	wantCode(t, "current version of another site", err, foreignKeyViolation)
}

func TestOAuthGrantRevocation(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	user := newUser(t, pool)
	client, grant, family := newID(), newID(), newID()
	exec(t, pool, `INSERT INTO oauth_clients (id, client_id, registration, metadata, redirect_uris, fetched_at)
		VALUES ($1, 'https://client.example/meta.json', 'cimd', '{}', '{http://127.0.0.1/cb}', now())`, client)
	exec(t, pool, "INSERT INTO oauth_grants (id, user_id, client_id, scopes) VALUES ($1, $2, $3, '{mcp}')", grant, user, client)
	exec(t, pool, `INSERT INTO oauth_authorization_codes (id, token_hash, grant_id, redirect_uri, code_challenge, resource, expires_at)
		VALUES ($1, $2, $3, 'http://127.0.0.1/cb', 'challenge', 'https://dash.example.com/mcp', now() + interval '1 minute')`, family, sha("code"), grant)
	exec(t, pool, `INSERT INTO oauth_access_tokens (id, token_hash, grant_id, family_id, resource, expires_at)
		VALUES ($1, $2, $3, $4, 'https://dash.example.com/mcp', now() + interval '1 hour')`, newID(), sha("access"), grant, family)
	first := newID()
	exec(t, pool, `INSERT INTO oauth_refresh_tokens (id, token_hash, grant_id, family_id, resource, expires_at)
		VALUES ($1, $2, $3, $4, 'https://dash.example.com/mcp', now() + interval '30 days')`, first, sha("refresh1"), grant, family)
	exec(t, pool, `INSERT INTO oauth_refresh_tokens (id, token_hash, grant_id, family_id, parent_id, resource, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'https://dash.example.com/mcp', now() + interval '30 days')`, newID(), sha("refresh2"), grant, family, first)
	_, err := pool.Exec(t.Context(), `INSERT INTO oauth_refresh_tokens (id, token_hash, grant_id, family_id, parent_id, resource, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'https://dash.example.com/mcp', now() + interval '30 days')`, newID(), sha("refresh3"), grant, family, first)
	wantCode(t, "second rotation of one refresh token", err, uniqueViolation)

	_, err = pool.Exec(t.Context(), `INSERT INTO oauth_clients (id, client_id, registration, metadata, redirect_uris)
		VALUES ($1, 'https://other.example/meta.json', 'cimd', '{}', '{}')`, newID())
	wantCode(t, "CIMD client without fetch time", err, checkViolation)

	exec(t, pool, "DELETE FROM oauth_grants WHERE id = $1", grant)
	for _, table := range []string{"oauth_authorization_codes", "oauth_access_tokens", "oauth_refresh_tokens"} {
		if n := count(t, pool, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s rows left after revoking the grant: %d", table, n)
		}
	}
}
