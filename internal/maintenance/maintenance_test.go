package maintenance

import (
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/storage"
)

type fixture struct {
	t     *testing.T
	d     *server.Deps
	r     *Runner
	svc   *sites.Service
	user  uuid.UUID
	email uuid.UUID
	site  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := sitestest.Deps(t)
	f := &fixture{t: t, d: d, r: New(d), svc: sites.NewService(d), user: sitestest.User(t, d, "owner@example.com")}
	f.scan(`SELECT id FROM emails WHERE user_id = $1`, []any{f.user}, &f.email)
	s, err := f.svc.Create(t.Context(), f.user, "Site", "")
	if err != nil {
		t.Fatal(err)
	}
	f.site = s.ID
	return f
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.d.DB.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fixture) scan(sql string, args []any, dest ...any) {
	f.t.Helper()
	if err := f.d.DB.QueryRow(f.t.Context(), sql, args...).Scan(dest...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fixture) run() {
	f.t.Helper()
	ran, err := f.r.RunOnce(f.t.Context())
	if err != nil || !ran {
		f.t.Fatalf("RunOnce: ran=%v err=%v", ran, err)
	}
}

// left returns the ids remaining in table.
func (f *fixture) left(table string) []uuid.UUID {
	f.t.Helper()
	rows, err := f.d.DB.Query(f.t.Context(), "SELECT id FROM "+table)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func wantLeft(t *testing.T, table string, got []uuid.UUID, keep, gone []uuid.UUID) {
	t.Helper()
	for _, id := range keep {
		if !slices.Contains(got, id) {
			t.Errorf("%s: row %s was deleted, want kept", table, id)
		}
	}
	for _, id := range gone {
		if slices.Contains(got, id) {
			t.Errorf("%s: row %s was kept, want deleted", table, id)
		}
	}
}

const hash = `sha256(gen_random_uuid()::text::bytea)`

// Ages relative to the expiry a row has: past the grace period, within
// it, and not yet expired.
const (
	stale  = "now() - interval '2 hours'"
	recent = "now() - interval '30 minutes'"
	live   = "now() + interval '1 hour'"
)

func TestExpiredRows(t *testing.T) {
	f := newFixture(t)
	type table struct {
		name   string
		insert string // $1 id, $2 expires_at expression substituted
		args   []any
	}
	clientID := uuid.New()
	f.exec(`INSERT INTO oauth_clients (id, client_id, registration, metadata, redirect_uris) VALUES ($1, 'dcr-live', 'dcr', '{}', '{http://localhost/cb}')`, clientID)
	grantID := uuid.New()
	f.exec(`INSERT INTO oauth_grants (id, user_id, client_id) VALUES ($1, $2, $3)`, grantID, f.user, clientID)
	tables := []table{
		{"sessions", `INSERT INTO sessions (id, token_hash, user_id, expires_at) VALUES ($1, ` + hash + `, $2, EXPIRES)`, []any{f.user}},
		{"site_tickets", `INSERT INTO site_tickets (id, token_hash, site_id, user_id, expires_at, consumed_at) VALUES ($1, ` + hash + `, $2, $3, EXPIRES, now())`, []any{f.site, f.user}},
		{"auth_flows", `INSERT INTO auth_flows (id, token_hash, kind, data, expires_at) VALUES ($1, ` + hash + `, 'passkey_login', '{}', EXPIRES)`, nil},
		{"email_verifications", `INSERT INTO email_verifications (id, token_hash, email_id, expires_at) VALUES ($1, ` + hash + `, $2, EXPIRES)`, []any{f.email}},
		{"oauth_authorization_requests", `INSERT INTO oauth_authorization_requests (id, token_hash, user_id, client_id, redirect_uri, code_challenge, resource, expires_at) VALUES ($1, ` + hash + `, $2, $3, 'http://localhost/cb', 'c', 'r', EXPIRES)`, []any{f.user, clientID}},
		{"oauth_authorization_codes", `INSERT INTO oauth_authorization_codes (id, token_hash, grant_id, redirect_uri, code_challenge, resource, expires_at, consumed_at) VALUES ($1, ` + hash + `, $2, 'http://localhost/cb', 'c', 'r', EXPIRES, now())`, []any{grantID}},
		{"oauth_access_tokens", `INSERT INTO oauth_access_tokens (id, token_hash, grant_id, family_id, resource, expires_at) VALUES ($1, ` + hash + `, $2, gen_random_uuid(), 'r', EXPIRES)`, []any{grantID}},
		{"oauth_refresh_tokens", `INSERT INTO oauth_refresh_tokens (id, token_hash, grant_id, family_id, resource, expires_at, rotated_at) VALUES ($1, ` + hash + `, $2, gen_random_uuid(), 'r', EXPIRES, now())`, []any{grantID}},
	}
	ids := map[string][3]uuid.UUID{}
	for _, tb := range tables {
		var row [3]uuid.UUID
		for i, exp := range []string{stale, recent, live} {
			row[i] = uuid.New()
			f.exec(strings.Replace(tb.insert, "EXPIRES", exp, 1), append([]any{row[i]}, tb.args...)...)
		}
		ids[tb.name] = row
	}
	f.run()
	for _, tb := range tables {
		row := ids[tb.name]
		wantLeft(t, tb.name, f.left(tb.name), row[1:], row[:1])
	}
	// The client has a grant: kept.
	wantLeft(t, "oauth_clients", f.left("oauth_clients"), []uuid.UUID{clientID}, nil)
}

func TestUnusedOAuthClients(t *testing.T) {
	f := newFixture(t)
	mk := func(registration, created string, cacheExpires string) uuid.UUID {
		id := uuid.New()
		if registration == "dcr" {
			f.exec(`INSERT INTO oauth_clients (id, client_id, registration, metadata, redirect_uris, created_at) VALUES ($1, $2, 'dcr', '{}', '{http://localhost/cb}', `+created+`)`, id, id.String())
		} else {
			f.exec(`INSERT INTO oauth_clients (id, client_id, registration, metadata, redirect_uris, created_at, fetched_at, cache_expires_at) VALUES ($1, $2, 'cimd', '{}', '{http://localhost/cb}', `+created+`, now(), `+cacheExpires+`)`, id, "https://"+id.String()+".example/client.json")
		}
		return id
	}
	old := "now() - interval '2 days'"
	dcrOld := mk("dcr", old, "")
	dcrNew := mk("dcr", "now()", "")
	dcrGranted := mk("dcr", old, "")
	f.exec(`INSERT INTO oauth_grants (id, user_id, client_id) VALUES ($1, $2, $3)`, uuid.New(), f.user, dcrGranted)
	dcrPending := mk("dcr", old, "")
	f.exec(`INSERT INTO oauth_authorization_requests (id, token_hash, user_id, client_id, redirect_uri, code_challenge, resource, expires_at) VALUES ($1, `+hash+`, $2, $3, 'http://localhost/cb', 'c', 'r', now() + interval '5 minutes')`, uuid.New(), f.user, dcrPending)
	cimdExpired := mk("cimd", old, old)
	cimdCached := mk("cimd", old, "now() + interval '1 hour'")
	f.run()
	wantLeft(t, "oauth_clients", f.left("oauth_clients"),
		[]uuid.UUID{dcrNew, dcrGranted, dcrPending, cimdCached}, []uuid.UUID{dcrOld, cimdExpired})
}

func TestInvitesAndTransfers(t *testing.T) {
	f := newFixture(t)
	retained := "now() - interval '10 days'"
	past := "now() - interval '31 days'"
	for _, table := range []string{"invites", "ownership_transfers"} {
		insert := func(expires, state string) uuid.UUID {
			id := uuid.New()
			cols, vals := "id, token_hash, site_id, email, expires_at", "$1, "+hash+", $2, 'x@example.com', "+expires
			if table == "invites" {
				cols, vals = cols+", role", vals+", 'viewer'"
			}
			switch state {
			case "consumed":
				cols, vals = cols+", consumed_at", vals+", now() - interval '40 days'"
			case "revoked":
				cols, vals = cols+", revoked_at", vals+", now() - interval '40 days'"
			}
			f.exec("INSERT INTO "+table+" ("+cols+") VALUES ("+vals+")", id, f.site)
			return id
		}
		pendingGone := insert(past, "")
		revokedGone := insert(past, "revoked")
		consumedKept := insert(past, "consumed")
		expiredKept := insert(retained, "")
		liveKept := insert(live, "")
		f.run()
		wantLeft(t, table, f.left(table), []uuid.UUID{consumedKept, expiredKept, liveKept}, []uuid.UUID{pendingGone, revokedGone})
	}
}

func (f *fixture) deployment(mode sites.Mode, files ...sites.File) uuid.UUID {
	f.t.Helper()
	dep, err := f.svc.CreateDeployment(f.t.Context(), f.site, f.user, mode)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.svc.AddFiles(f.t.Context(), dep, f.user, files); err != nil {
		f.t.Fatal(err)
	}
	return dep
}

func file(p, body string) sites.File { return sites.File{Path: p, Body: []byte(body)} }

func sum(body string) []byte { s := sha256.Sum256([]byte(body)); return s[:] }

// blob reports whether the site still has the blob's row and object.
func (f *fixture) blob(body string) (row, object bool) {
	f.t.Helper()
	f.scan(`SELECT EXISTS (SELECT 1 FROM site_blobs WHERE site_id = $1 AND sha256 = $2)`, []any{f.site, sum(body)}, &row)
	_, err := f.d.Storage.Stat(f.t.Context(), sites.BlobKey(f.site, sum(body)))
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		f.t.Fatal(err)
	}
	return row, err == nil
}

func (f *fixture) age(body string, d string) {
	f.exec(`UPDATE site_blobs SET staged_at = now() - $3::interval WHERE site_id = $1 AND sha256 = $2`, f.site, sum(body), d)
}

func TestDeploymentsAndBlobGC(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	// "published" is in the current version; "old-version" only in an
	// earlier one (rollback keeps it reachable); "staged" in an open
	// deployment; "abandoned" only in an abandoned deployment; "orphan" in
	// a deployment open for over a day; "fresh" is unreferenced but was
	// staged within the grace period; "reused" is unreferenced and old
	// until a deployment (abandoned since) stages it again, refreshing it.
	if _, err := f.svc.Publish(ctx, f.site, f.user, sites.Replace, []sites.File{file("a.html", "old-version")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Publish(ctx, f.site, f.user, sites.Replace, []sites.File{file("a.html", "published")}, nil); err != nil {
		t.Fatal(err)
	}
	staged := f.deployment(sites.Replace, file("b.html", "staged"))
	abandoned := f.deployment(sites.Replace, file("c.html", "abandoned"))
	f.exec(`UPDATE deployments SET state = 'abandoned', updated_at = now() - interval '8 days' WHERE id = $1`, abandoned)
	orphan := f.deployment(sites.Replace, file("d.html", "orphan"))
	f.exec(`UPDATE deployments SET created_at = now() - interval '25 hours' WHERE id = $1`, orphan)
	recentlyAbandoned := f.deployment(sites.Replace, file("e.html", "fresh"))
	f.exec(`UPDATE deployments SET state = 'abandoned' WHERE id = $1`, recentlyAbandoned)
	reused := f.deployment(sites.Replace, file("f.html", "reused"))
	f.exec(`UPDATE deployments SET state = 'abandoned' WHERE id = $1`, reused)
	for _, b := range []string{"old-version", "published", "staged", "abandoned", "orphan", "reused"} {
		f.age(b, "2 days")
	}
	// Staging an old blob again refreshes it.
	again := f.deployment(sites.Replace, file("g.html", "reused"))
	f.exec(`UPDATE deployments SET state = 'abandoned' WHERE id = $1`, again)

	f.run()

	var states = map[uuid.UUID]string{}
	rows, err := f.d.DB.Query(ctx, `SELECT id, state FROM deployments`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			t.Fatal(err)
		}
		states[id] = s
	}
	if _, ok := states[abandoned]; ok {
		t.Error("deployment abandoned 8 days ago was kept")
	}
	if states[orphan] != "abandoned" || states[staged] != "open" || states[again] != "abandoned" || states[recentlyAbandoned] != "abandoned" {
		t.Errorf("deployment states: %v", states)
	}

	for body, want := range map[string]bool{
		"old-version": true, "published": true, "staged": true, "fresh": true, "reused": true,
		"abandoned": false,
		// The orphan was abandoned in this pass and its blob is old: gone.
		"orphan": false,
	} {
		row, obj := f.blob(body)
		if row != want || obj != want {
			t.Errorf("blob %q: row=%v object=%v, want %v", body, row, obj, want)
		}
	}

	// A collected blob is stored again when it is published again.
	if _, err := f.svc.Publish(ctx, f.site, f.user, sites.Replace, []sites.File{file("c.html", "abandoned")}, nil); err != nil {
		t.Fatal(err)
	}
	if row, obj := f.blob("abandoned"); !row || !obj {
		t.Fatalf("republished blob: row=%v object=%v", row, obj)
	}
	if files := f.contents(); files["c.html"] != "abandoned" {
		t.Fatalf("republished site: %v", files)
	}
}

func (f *fixture) contents() map[string]string {
	f.t.Helper()
	fis, err := f.svc.ListFiles(f.t.Context(), f.site)
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]string{}
	for _, fi := range fis {
		obj, _, err := f.svc.OpenFile(f.t.Context(), f.site, fi.Path)
		if err != nil {
			f.t.Fatal(err)
		}
		b, err := io.ReadAll(obj.Body)
		obj.Body.Close()
		if err != nil {
			f.t.Fatal(err)
		}
		out[fi.Path] = string(b)
	}
	return out
}

func TestBlobGCGraceIsConfigured(t *testing.T) {
	f := newFixture(t)
	f.r.blobGC = 3 * time.Hour
	dep := f.deployment(sites.Replace, file("a.html", "within"), file("b.html", "beyond"))
	f.exec(`UPDATE deployments SET state = 'abandoned' WHERE id = $1`, dep)
	f.age("within", "2 hours")
	f.age("beyond", "4 hours")
	f.run()
	if row, obj := f.blob("within"); !row || !obj {
		t.Errorf("blob inside the grace period: row=%v object=%v", row, obj)
	}
	if row, obj := f.blob("beyond"); row || obj {
		t.Errorf("blob past the grace period: row=%v object=%v", row, obj)
	}
}

func TestStoragePrefixesAreRetried(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	prefix := "sites/abcdefghijkl/"
	if err := f.d.Storage.Put(ctx, prefix+"blobs/x", strings.NewReader("x"), 1, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if err := db.New(f.d.DB).QueueStorageDeletion(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	f.run()
	if _, err := f.d.Storage.Stat(ctx, prefix+"blobs/x"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("queued prefix not deleted: %v", err)
	}
	var n int
	f.scan(`SELECT count(*) FROM storage_deletions`, nil, &n)
	if n != 0 {
		t.Fatalf("%d queued prefixes left", n)
	}
}

func TestOnlyOneReplicaRuns(t *testing.T) {
	f := newFixture(t)
	conn, err := f.d.DB.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), "SELECT pg_advisory_lock($1)", lockID); err != nil {
		t.Fatal(err)
	}
	if ran, err := f.r.RunOnce(t.Context()); ran || err != nil {
		t.Fatalf("RunOnce while another replica holds the lock: ran=%v err=%v", ran, err)
	}
	if _, err := conn.Exec(t.Context(), "SELECT pg_advisory_unlock($1)", lockID); err != nil {
		t.Fatal(err)
	}
	f.run()
	// The lock is released after a run.
	f.run()
}
