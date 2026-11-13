// Package sitestest builds server dependencies over a fresh test database
// (dbtest) and a local storage directory, and seeds accounts, for tests of
// the sites service and the packages built on it.
package sitestest

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/db/dbtest"
	"github.com/mjquinlan2000/agent-dash/internal/server"
	"github.com/mjquinlan2000/agent-dash/internal/storage/local"
)

// AppOrigin and SitesDomain are the test configuration's origins.
const (
	AppOrigin   = "http://dash.localhost:8084"
	SitesDomain = "sites.localhost"
)

// Deps returns dependencies over a fresh migrated database and an empty
// local store; it skips like dbtest.Pool when no test database is set.
func Deps(t *testing.T) *server.Deps {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			"APP_ORIGIN":   AppOrigin,
			"SITES_DOMAIN": SitesDomain,
			"SECRET_KEY":   strings.Repeat("s", 32),
			"DATABASE_URL": "postgres://unused/x",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.Pool(t)
	store, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &server.Deps{Config: cfg, Logger: slog.New(slog.DiscardHandler), DB: pool, Storage: store}
}

// User creates an account with a verified primary email and returns its id.
func User(t *testing.T, d *server.Deps, email string) uuid.UUID {
	t.Helper()
	q := db.New(d.DB)
	u, err := q.CreateUser(t.Context(), uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatal(err)
	}
	e, err := q.CreateEmail(t.Context(), db.CreateEmailParams{ID: uuid.Must(uuid.NewV7()), UserID: u.ID, Email: email, IsPrimary: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkEmailVerified(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// Member gives user a non-owner role on a site.
func Member(t *testing.T, d *server.Deps, siteID string, user uuid.UUID, role string) {
	t.Helper()
	if err := db.New(d.DB).CreateMembership(t.Context(), db.CreateMembershipParams{SiteID: siteID, UserID: user, Role: role}); err != nil {
		t.Fatal(err)
	}
}
