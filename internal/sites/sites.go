// Package sites is the site service: sites and memberships, staged and
// one-shot publishing into immutable versions, rollback, and the file
// lookups the site file server (internal/siteserver) streams from.
//
// Blobs are content-addressed per site at sites/<site-id>/blobs/<sha256-hex>
// (docs/design.md#storage); versions and deployments only hold rows that
// point at them, so merge publishes and rollbacks copy rows, never objects.
//
// The service does not check roles: callers (the UI's /app-api, MCP tools)
// decide with rbac.Can before calling it.
package sites

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/siteid"
	"github.com/ferrule-io/handbill/internal/storage"
)

var (
	// ErrNotFound is returned when a site, version, deployment, or file does
	// not exist.
	ErrNotFound = errors.New("sites: not found")
	// ErrDeploymentClosed is returned when files are added to, or a
	// finalize is attempted on, a deployment that is no longer open.
	ErrDeploymentClosed = errors.New("sites: deployment is not open")
)

// InputError is caller input the service rejects (a bad path, title, mode,
// archive entry, or an empty deployment). Its message is safe to show to
// the caller.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputErrorf(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// Service is stateless over the pool and store; construct one per module.
type Service struct {
	db    *pgxpool.Pool
	store storage.Store
}

// NewService returns a Service over d.DB and d.Storage.
func NewService(d *server.Deps) *Service {
	return &Service{db: d.DB, store: d.Storage}
}

// Site is a site's metadata.
type Site struct {
	ID, Title, Description string
	OwnerID                uuid.UUID
	CurrentVersionID       uuid.NullUUID
	CreatedAt, UpdatedAt   time.Time
}

// Membership is a site together with one user's role on it.
type Membership struct {
	Site Site
	Role rbac.Role
}

func siteFromRow(r db.Site) Site {
	return Site{
		ID:               r.ID,
		Title:            r.Title,
		Description:      r.Description,
		OwnerID:          r.OwnerID,
		CurrentVersionID: r.CurrentVersionID,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

// Limits on site metadata, in characters.
const (
	MaxTitleLen       = 200
	MaxDescriptionLen = 2000
)

func cleanTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", inputErrorf("title is required")
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxTitleLen || strings.ContainsFunc(s, unicode.IsControl) {
		return "", inputErrorf("title must be at most %d characters of text without control characters", MaxTitleLen)
	}
	return s, nil
}

func cleanDescription(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxDescriptionLen {
		return "", inputErrorf("description must be at most %d characters", MaxDescriptionLen)
	}
	if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return "", inputErrorf("description must not contain control characters")
	}
	return s, nil
}

// Create creates a site owned by owner, with a fresh random id, and the
// owner's membership in the same transaction.
func (s *Service) Create(ctx context.Context, owner uuid.UUID, title, description string) (Site, error) {
	title, err := cleanTitle(title)
	if err != nil {
		return Site{}, err
	}
	description, err = cleanDescription(description)
	if err != nil {
		return Site{}, err
	}
	var site db.Site
	err = db.InTx(ctx, s.db, func(q *db.Queries) error {
		var err error
		site, err = q.CreateSite(ctx, db.CreateSiteParams{
			ID: siteid.New(), Title: title, Description: description, OwnerID: owner,
		})
		if err != nil {
			return err
		}
		return q.CreateMembership(ctx, db.CreateMembershipParams{SiteID: site.ID, UserID: owner, Role: string(rbac.Owner)})
	})
	if err != nil {
		return Site{}, fmt.Errorf("sites: create: %w", err)
	}
	return siteFromRow(site), nil
}

// Get returns a site, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Site, error) {
	if !siteid.Valid(id) {
		return Site{}, ErrNotFound
	}
	r, err := db.New(s.db).GetSite(ctx, id)
	if err != nil {
		return Site{}, notFound(err)
	}
	return siteFromRow(r), nil
}

// Update changes the title and/or description; nil leaves a field as is.
func (s *Service) Update(ctx context.Context, id string, title, description *string) (Site, error) {
	p := db.UpdateSiteParams{ID: id}
	if title != nil {
		t, err := cleanTitle(*title)
		if err != nil {
			return Site{}, err
		}
		p.Title = &t
	}
	if description != nil {
		d, err := cleanDescription(*description)
		if err != nil {
			return Site{}, err
		}
		p.Description = &d
	}
	r, err := db.New(s.db).UpdateSite(ctx, p)
	if err != nil {
		return Site{}, notFound(err)
	}
	return siteFromRow(r), nil
}

// Delete deletes the site with everything that belongs to it in the
// database, then its storage prefix sites/<id>/. Viewers stop seeing it as
// soon as the database delete commits; if storage cleanup fails the error
// is returned and the orphaned objects are left for blob GC.
func (s *Service) Delete(ctx context.Context, id string) error {
	if !siteid.Valid(id) {
		return ErrNotFound
	}
	n, err := db.New(s.db).DeleteSite(ctx, id)
	if err != nil {
		return fmt.Errorf("sites: delete: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := s.store.DeletePrefix(ctx, sitePrefix(id)); err != nil {
		return fmt.Errorf("sites: delete storage for %s: %w", id, err)
	}
	return nil
}

// ListForUser returns every site user has a role on, most recently
// updated first.
func (s *Service) ListForUser(ctx context.Context, user uuid.UUID) ([]Membership, error) {
	rows, err := db.New(s.db).ListSitesForUser(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("sites: list: %w", err)
	}
	out := make([]Membership, len(rows))
	for i, r := range rows {
		out[i] = Membership{Site: siteFromRow(r.Site), Role: rbac.Role(r.Role)}
	}
	return out, nil
}

// Role returns user's role on a site, or "" when they have none (including
// when the site does not exist).
func (s *Service) Role(ctx context.Context, siteID string, user uuid.UUID) (rbac.Role, error) {
	if !siteid.Valid(siteID) {
		return "", nil
	}
	role, err := db.New(s.db).GetMemberRole(ctx, db.GetMemberRoleParams{SiteID: siteID, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sites: role: %w", err)
	}
	return rbac.Role(role), nil
}

// Access is the per-request state the file server checks a site cookie against.
type Access struct {
	SiteID            string
	CurrentVersionID  uuid.NullUUID
	Role              rbac.Role // for the cookie's user; "" when none
	PublicLinkEnabled bool
	PublicLinkGen     int64
}

// Access loads a site's access state for user (uuid.Nil for public-link
// cookies) in one query, or ErrNotFound.
func (s *Service) Access(ctx context.Context, siteID string, user uuid.UUID) (Access, error) {
	if !siteid.Valid(siteID) {
		return Access{}, ErrNotFound
	}
	r, err := db.New(s.db).GetSiteAccess(ctx, db.GetSiteAccessParams{SiteID: siteID, UserID: user})
	if err != nil {
		return Access{}, notFound(err)
	}
	a := Access{SiteID: r.ID, CurrentVersionID: r.CurrentVersionID}
	if r.Role != nil {
		a.Role = rbac.Role(*r.Role)
	}
	if r.PublicLinkEnabled != nil && r.PublicLinkGeneration != nil {
		a.PublicLinkEnabled = *r.PublicLinkEnabled
		a.PublicLinkGen = *r.PublicLinkGeneration
	}
	return a, nil
}

// notFound maps a missing row to ErrNotFound and wraps anything else.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("sites: %w", err)
}

func isForeignKeyViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

func sitePrefix(siteID string) string { return "sites/" + siteID + "/" }
