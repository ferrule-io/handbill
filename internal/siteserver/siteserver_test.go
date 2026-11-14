package siteserver_test

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/siteserver"
	"github.com/ferrule-io/handbill/internal/token"
)

type env struct {
	t      *testing.T
	d      *server.Deps
	h      http.Handler
	svc    *sites.Service
	signer *token.Signer
	owner  uuid.UUID
	site   sites.Site
}

// testUserHeader stands in for the session middleware: it signs in as the
// user id it carries.
const testUserHeader = "X-Test-User"

func newEnv(t *testing.T) *env {
	t.Helper()
	d := sitestest.Deps(t)
	h, err := server.New(d, siteserver.Register, func(r *server.Routes, d *server.Deps) error {
		r.UseApp(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if id, err := uuid.Parse(req.Header.Get(testUserHeader)); err == nil {
					req = req.WithContext(identity.WithUser(req.Context(), identity.User{ID: id}))
				}
				next.ServeHTTP(w, req)
			})
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, d: d, h: h, svc: sites.NewService(d), signer: token.NewSigner(d.Config.SecretKey)}
	e.owner = sitestest.User(t, d, "owner@example.com")
	e.site = e.newSite(map[string]string{
		"index.html":      "<h1>home</h1>",
		"about.html":      "<h1>about</h1>",
		"docs/index.html": "<h1>docs</h1>",
		"docs/guide":      "guide without extension",
		"app.css":         "h1{color:red}",
		"healthz":         "site healthz file",
		"404.html":        "<h1>custom 404</h1>",
	})
	return e
}

func (e *env) newSite(files map[string]string) sites.Site {
	e.t.Helper()
	s, err := e.svc.Create(e.t.Context(), e.owner, "Site", "")
	if err != nil {
		e.t.Fatal(err)
	}
	if files != nil {
		e.publish(s.ID, sites.Replace, files)
	}
	return s
}

func (e *env) publish(siteID string, mode sites.Mode, files map[string]string) sites.Version {
	e.t.Helper()
	var fs []sites.File
	for p, b := range files {
		fs = append(fs, sites.File{Path: p, Body: []byte(b)})
	}
	v, err := e.svc.Publish(e.t.Context(), siteID, e.owner, mode, fs, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func siteHost(id string) string { return id + "." + sitestest.SitesDomain + ":8084" }

// req sends a request to rawURL (absolute, Host from the URL).
type req struct {
	method  string
	url     string
	user    uuid.UUID
	cookie  *http.Cookie
	headers map[string]string
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	e.t.Helper()
	if r.method == "" {
		r.method = http.MethodGet
	}
	hr := httptest.NewRequest(r.method, r.url, nil)
	if r.user != uuid.Nil {
		hr.Header.Set(testUserHeader, r.user.String())
	}
	if r.cookie != nil {
		hr.AddCookie(r.cookie)
	}
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, hr)
	return rec
}

func (e *env) siteURL(id, path string) string { return "http://" + siteHost(id) + path }

// cookieFor signs a member cookie directly.
func (e *env) cookieFor(siteID string, user uuid.UUID) *http.Cookie {
	v := e.signer.SiteCookie(token.SiteCookie{SiteID: siteID, UserID: user, Expires: time.Now().Add(time.Hour)})
	return &http.Cookie{Name: siteserver.CookieName, Value: v}
}

func location(t *testing.T, rec *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d, want 302; body %q", rec.Code, rec.Body)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// signIn runs the whole handoff for user and returns the site cookie.
func (e *env) signIn(siteID string, user uuid.UUID, path string) *http.Cookie {
	e.t.Helper()
	toApp := location(e.t, e.do(req{url: e.siteURL(siteID, path)}))
	issue := location(e.t, e.do(req{url: toApp.String(), user: user}))
	if issue.Host != siteHost(siteID) || issue.Path != "/_auth/callback" {
		e.t.Fatalf("ticket redirect to %s", issue)
	}
	rec := e.do(req{url: issue.String()})
	if got := location(e.t, rec).String(); got != path {
		e.t.Fatalf("callback returns to %q, want %q", got, path)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == siteserver.CookieName {
			return c
		}
	}
	e.t.Fatal("no site cookie set")
	return nil
}

func TestHandoffEndToEnd(t *testing.T) {
	e := newEnv(t)
	id := e.site.ID

	// 1. No cookie: the site host sends the visitor to the app.
	rec := e.do(req{url: e.siteURL(id, "/about?tab=2")})
	toApp := location(t, rec)
	if toApp.Scheme+"://"+toApp.Host != sitestest.AppOrigin || toApp.Path != "/auth/site" ||
		toApp.Query().Get("site") != id || toApp.Query().Get("return") != "/about?tab=2" {
		t.Fatalf("handoff redirect %s", toApp)
	}

	// 2. Without a session the app asks for sign-in, returning here after.
	login := location(t, e.do(req{url: toApp.String()}))
	if login.Path != "/login" || login.Query().Get("return") != toApp.RequestURI() {
		t.Fatalf("login redirect %s", login)
	}

	// 3. With a session and a role: a ticket for the site host.
	rec = e.do(req{url: toApp.String(), user: e.owner})
	issue := location(t, rec)
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || issue.Host != siteHost(id) || issue.Path != "/_auth/callback" || issue.Query().Get("ticket") == "" {
		t.Fatalf("ticket redirect %s", issue)
	}

	// 4. The callback sets the cookie and returns to the path.
	rec = e.do(req{url: issue.String()})
	if got := location(t, rec).String(); got != "/about?tab=2" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback redirect %q", got)
	}
	// Issuing a cookie clears the origin's storage (service workers
	// included) but not the cookie it sets.
	if got := rec.Header().Get("Clear-Site-Data"); got != `"storage"` {
		t.Fatalf("callback Clear-Site-Data %q", got)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == siteserver.CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != int(siteserver.CookieTTL/time.Second) {
		t.Fatalf("site cookie %+v", cookie)
	}

	// 5. The cookie admits the owner.
	rec = e.do(req{url: e.siteURL(id, "/about?tab=2"), cookie: cookie})
	if rec.Code != 200 || rec.Body.String() != "<h1>about</h1>" {
		t.Fatalf("with cookie: %d %q", rec.Code, rec.Body)
	}

	// The ticket is single use.
	rec = e.do(req{url: issue.String()})
	if rec.Code != http.StatusBadRequest || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("replayed ticket: %d", rec.Code)
	}
	// A cookie for one site does not open another.
	other := e.newSite(map[string]string{"index.html": "other"})
	rec = e.do(req{url: e.siteURL(other.ID, "/"), cookie: cookie})
	if rec.Code != http.StatusFound {
		t.Fatalf("cookie on another site: %d %q", rec.Code, rec.Body)
	}
}

// Site content can never register a service worker: it would intercept
// /_auth/ navigations and outlive the visitor's access.
func TestServiceWorkerScriptsRefused(t *testing.T) {
	e := newEnv(t)
	s := e.newSite(map[string]string{"index.html": "home", "sw.js": "self.onfetch = () => {}"})
	cookie := e.cookieFor(s.ID, e.owner)
	if rec := e.do(req{url: e.siteURL(s.ID, "/sw.js"), cookie: cookie}); rec.Code != http.StatusOK {
		t.Fatalf("plain fetch: %d", rec.Code)
	}
	for _, c := range []*http.Cookie{cookie, nil} {
		rec := e.do(req{url: e.siteURL(s.ID, "/sw.js"), cookie: c, headers: map[string]string{"Service-Worker": "script"}})
		if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "onfetch") {
			t.Fatalf("service worker script (cookie %v): %d %q", c != nil, rec.Code, rec.Body)
		}
	}
}

// The public link's opener page carries the token only in the fragment;
// its CSP admits exactly its own script and a same-origin form post.
func TestShareOpenerPage(t *testing.T) {
	e := newEnv(t)
	rec := e.do(req{url: e.siteURL(e.site.ID, "/_auth/share")})
	body := rec.Body.String()
	if rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 || !strings.Contains(body, `<form id="share" method="post" action="/_auth/share">`) {
		t.Fatalf("opener: %d %q", rec.Code, body)
	}
	_, script, ok := strings.Cut(body, "<script>")
	script, _, ok2 := strings.Cut(script, "</script>")
	if !ok || !ok2 || strings.Count(body, "<script") != 1 {
		t.Fatalf("opener scripts: %q", body)
	}
	sum := sha256.Sum256([]byte(script))
	csp := rec.Header().Get("Content-Security-Policy")
	want := "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "';"
	if !strings.Contains(csp, want) || !strings.Contains(csp, "form-action 'self'") || !strings.Contains(csp, "default-src 'none'") ||
		!strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("opener CSP %q, want %q", csp, want)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("opener headers %v", rec.Header())
	}
}

func (e *env) ticketURL(siteID string, user uuid.UUID, ret string) *url.URL {
	e.t.Helper()
	q := url.Values{"site": {siteID}, "return": {ret}}
	return location(e.t, e.do(req{url: sitestest.AppOrigin + "/auth/site?" + q.Encode(), user: user}))
}

func TestTicketExpiryAndWrongSite(t *testing.T) {
	e := newEnv(t)
	other := e.newSite(map[string]string{"index.html": "other"})

	// A ticket for one site is refused (and burned) on another site's host.
	issue := e.ticketURL(e.site.ID, e.owner, "/")
	wrong := "http://" + siteHost(other.ID) + issue.RequestURI()
	if rec := e.do(req{url: wrong}); rec.Code != http.StatusBadRequest || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("ticket on wrong site: %d", rec.Code)
	}
	if rec := e.do(req{url: issue.String()}); rec.Code != http.StatusBadRequest {
		t.Fatalf("ticket after wrong-site attempt: %d", rec.Code)
	}

	// Expired tickets are refused.
	issue = e.ticketURL(e.site.ID, e.owner, "/")
	if _, err := e.d.DB.Exec(t.Context(), "UPDATE site_tickets SET expires_at = now() - interval '1 second' WHERE token_hash = $1",
		token.Hash(issue.Query().Get("ticket"))); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(req{url: issue.String()}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("expired ticket: %d %q", rec.Code, rec.Body)
	}
	// Tickets are stored hashed and live about a minute.
	var ttl float64
	issue = e.ticketURL(e.site.ID, e.owner, "/")
	if err := e.d.DB.QueryRow(t.Context(), "SELECT extract(epoch FROM expires_at - created_at) FROM site_tickets WHERE token_hash = $1",
		token.Hash(issue.Query().Get("ticket"))).Scan(&ttl); err != nil {
		t.Fatal(err)
	}
	if ttl < 55 || ttl > 65 {
		t.Fatalf("ticket TTL %vs", ttl)
	}
	// Garbage and missing tickets.
	for _, q := range []string{"?ticket=nope", ""} {
		if rec := e.do(req{url: e.siteURL(e.site.ID, "/_auth/callback"+q)}); rec.Code != http.StatusBadRequest {
			t.Fatalf("callback%s: %d", q, rec.Code)
		}
	}
}

func TestReturnPathValidation(t *testing.T) {
	for p, want := range map[string]bool{
		"/":                             true,
		"/a/b?c=d&e=f":                  true,
		"/%2F%2Fevil":                   true,
		"":                              false,
		"a":                             false,
		"//evil.example":                false,
		"/\\evil.example":               false,
		"\\\\evil":                      false,
		"https://evil.test/":            false,
		"/a\nb":                         false,
		"/a\tb":                         false,
		"/" + strings.Repeat("a", 3000): false,
	} {
		if got := siteserver.ValidReturnPath(p); got != want {
			t.Errorf("ValidReturnPath(%q) = %v, want %v", p, got, want)
		}
	}
	e := newEnv(t)
	for _, ret := range []string{"//evil.example/x", "https://evil.example/", "/\\evil"} {
		q := url.Values{"site": {e.site.ID}, "return": {ret}}
		rec := e.do(req{url: sitestest.AppOrigin + "/auth/site?" + q.Encode(), user: e.owner})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("return %q: %d", ret, rec.Code)
		}
	}
	rec := e.do(req{url: sitestest.AppOrigin + "/auth/site?site=NOPE", user: e.owner})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad site id: %d", rec.Code)
	}
	// An omitted return path means the site root.
	issue := location(t, e.do(req{url: sitestest.AppOrigin + "/auth/site?site=" + e.site.ID, user: e.owner}))
	if got := location(t, e.do(req{url: issue.String()})).String(); got != "/" {
		t.Errorf("default return %q", got)
	}
}

func TestNonMemberGetsNothing(t *testing.T) {
	e := newEnv(t)
	stranger := sitestest.User(t, e.d, "stranger@example.com")

	// The handoff answers identically for an existing and a missing site.
	missing := "zzzzzzzzzzzz"
	var locs []string
	for _, id := range []string{e.site.ID, missing} {
		q := url.Values{"site": {id}, "return": {"/"}}
		rec := e.do(req{url: sitestest.AppOrigin + "/auth/site?" + q.Encode(), user: stranger})
		loc := location(t, rec)
		if loc.Host != "" || loc.Path != "/site" || loc.Query().Get("id") != id {
			t.Fatalf("non-member handoff for %s: %s", id, loc)
		}
		locs = append(locs, strings.ReplaceAll(loc.String(), id, "ID"))
	}
	if locs[0] != locs[1] {
		t.Fatalf("existence leak: %q vs %q", locs[0], locs[1])
	}
	var tickets int
	if err := e.d.DB.QueryRow(t.Context(), "SELECT count(*) FROM site_tickets WHERE user_id = $1", stranger).Scan(&tickets); err != nil || tickets != 0 {
		t.Fatalf("tickets issued to a non-member: %d %v", tickets, err)
	}

	// A correctly signed cookie for a user without a role is not enough,
	// and site hosts behave the same for missing sites.
	for _, id := range []string{e.site.ID, missing} {
		for _, path := range []string{"/", "/about", "/404.html", "/nope"} {
			rec := e.do(req{url: e.siteURL(id, path), cookie: e.cookieFor(id, stranger)})
			if rec.Code != http.StatusFound || strings.Contains(rec.Body.String(), "<h1>") {
				t.Fatalf("stranger %s%s: %d %q", id, path, rec.Code, rec.Body)
			}
		}
	}
	// Forged and expired cookies.
	bad := []*http.Cookie{
		{Name: siteserver.CookieName, Value: "forged.value"},
		{Name: siteserver.CookieName, Value: e.signer.SiteCookie(token.SiteCookie{SiteID: e.site.ID, UserID: e.owner, Expires: time.Now().Add(-time.Minute)})},
		{Name: siteserver.CookieName, Value: token.NewSigner([]byte(strings.Repeat("o", 32))).SiteCookie(token.SiteCookie{SiteID: e.site.ID, UserID: e.owner, Expires: time.Now().Add(time.Hour)})},
	}
	for _, c := range bad {
		if rec := e.do(req{url: e.siteURL(e.site.ID, "/"), cookie: c}); rec.Code != http.StatusFound {
			t.Fatalf("bad cookie admitted: %d", rec.Code)
		}
	}

	// A viewer gets in; revoking the role takes effect on the next request.
	sitestest.Member(t, e.d, e.site.ID, stranger, "viewer")
	cookie := e.signIn(e.site.ID, stranger, "/")
	if rec := e.do(req{url: e.siteURL(e.site.ID, "/"), cookie: cookie}); rec.Code != 200 {
		t.Fatalf("viewer: %d", rec.Code)
	}
	if _, err := e.d.DB.Exec(t.Context(), "DELETE FROM memberships WHERE site_id = $1 AND user_id = $2", e.site.ID, stranger); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(req{url: e.siteURL(e.site.ID, "/"), cookie: cookie}); rec.Code != http.StatusFound {
		t.Fatalf("revoked viewer: %d", rec.Code)
	}
}

func TestPathResolution(t *testing.T) {
	e := newEnv(t)
	cookie := e.cookieFor(e.site.ID, e.owner)
	for path, want := range map[string]struct {
		status int
		body   string
		ct     string
	}{
		"/":           {200, "<h1>home</h1>", "text/html; charset=utf-8"},
		"/index.html": {200, "<h1>home</h1>", "text/html; charset=utf-8"},
		"/about":      {200, "<h1>about</h1>", "text/html; charset=utf-8"},
		"/about.html": {200, "<h1>about</h1>", "text/html; charset=utf-8"},
		"/docs/":      {200, "<h1>docs</h1>", "text/html; charset=utf-8"},
		"/docs/guide": {200, "guide without extension", "application/octet-stream"},
		"/app.css":    {200, "h1{color:red}", "text/css; charset=utf-8"},
		"/App.css":    {404, "<h1>custom 404</h1>", "text/html; charset=utf-8"},
		"/missing":    {404, "<h1>custom 404</h1>", "text/html; charset=utf-8"},
		"/about/":     {404, "<h1>custom 404</h1>", "text/html; charset=utf-8"},
		"/healthz":    {200, "site healthz file", "application/octet-stream"},
		"/readyz":     {404, "<h1>custom 404</h1>", "text/html; charset=utf-8"},
	} {
		rec := e.do(req{url: e.siteURL(e.site.ID, path), cookie: cookie})
		if rec.Code != want.status || rec.Body.String() != want.body || rec.Header().Get("Content-Type") != want.ct {
			t.Errorf("%s: %d %q %q; want %d %q %q", path, rec.Code, rec.Body, rec.Header().Get("Content-Type"), want.status, want.body, want.ct)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "private, no-cache" {
			t.Errorf("%s: headers %v", path, rec.Header())
		}
	}

	// A directory index without its slash redirects, keeping the query.
	for path, want := range map[string]string{"/docs": "/docs/", "/docs?x=1&y=2": "/docs/?x=1&y=2"} {
		rec := e.do(req{url: e.siteURL(e.site.ID, path), cookie: cookie})
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != want {
			t.Errorf("%s: %d %q, want 301 %q", path, rec.Code, rec.Header().Get("Location"), want)
		}
	}

	// Without a 404.html the server answers a plain 404.
	bare := e.newSite(map[string]string{"index.html": "bare"})
	rec := e.do(req{url: e.siteURL(bare.ID, "/nope"), cookie: e.cookieFor(bare.ID, e.owner)})
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "custom") {
		t.Errorf("plain 404: %d %q", rec.Code, rec.Body)
	}
	// Nothing published yet.
	empty := e.newSite(nil)
	if rec := e.do(req{url: e.siteURL(empty.ID, "/"), cookie: e.cookieFor(empty.ID, e.owner)}); rec.Code != 404 {
		t.Errorf("unpublished site: %d", rec.Code)
	}
	// /_auth/ is reserved even when content would match.
	for _, p := range []string{"/_auth/", "/_auth/anything", "/_auth/share/x"} {
		if rec := e.do(req{url: e.siteURL(e.site.ID, p), cookie: cookie}); rec.Code != 404 || strings.Contains(rec.Body.String(), "custom") {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body)
		}
	}
	// Other methods are not served.
	if rec := e.do(req{method: http.MethodPost, url: e.siteURL(e.site.ID, "/"), cookie: cookie}); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestETagAndHead(t *testing.T) {
	e := newEnv(t)
	cookie := e.cookieFor(e.site.ID, e.owner)
	rec := e.do(req{url: e.siteURL(e.site.ID, "/app.css"), cookie: cookie})
	etag := rec.Header().Get("ETag")
	if rec.Code != 200 || len(etag) != 66 || rec.Header().Get("Content-Length") != "13" {
		t.Fatalf("GET: %d etag %q len %q", rec.Code, etag, rec.Header().Get("Content-Length"))
	}
	for _, inm := range []string{etag, "W/" + etag, `"x", ` + etag, "*"} {
		rec = e.do(req{url: e.siteURL(e.site.ID, "/app.css"), cookie: cookie, headers: map[string]string{"If-None-Match": inm}})
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag {
			t.Fatalf("If-None-Match %s: %d", inm, rec.Code)
		}
	}
	rec = e.do(req{url: e.siteURL(e.site.ID, "/app.css"), cookie: cookie, headers: map[string]string{"If-None-Match": `"other"`}})
	if rec.Code != 200 {
		t.Fatalf("stale etag: %d", rec.Code)
	}
	rec = e.do(req{method: http.MethodHead, url: e.siteURL(e.site.ID, "/app.css"), cookie: cookie})
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "13" || rec.Header().Get("ETag") != etag || rec.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("HEAD: %d %v", rec.Code, rec.Header())
	}
	// Without a cookie HEAD redirects like GET.
	if rec := e.do(req{method: http.MethodHead, url: e.siteURL(e.site.ID, "/")}); rec.Code != http.StatusFound {
		t.Fatalf("HEAD without cookie: %d", rec.Code)
	}
	// 404.html responses carry no ETag.
	rec = e.do(req{url: e.siteURL(e.site.ID, "/nope"), cookie: cookie})
	if rec.Code != 404 || rec.Header().Get("ETag") != "" {
		t.Fatalf("404: %d etag %q", rec.Code, rec.Header().Get("ETag"))
	}
}

func TestRollbackSwitchesContent(t *testing.T) {
	e := newEnv(t)
	cookie := e.cookieFor(e.site.ID, e.owner)
	get := func(path string) string {
		rec := e.do(req{url: e.siteURL(e.site.ID, path), cookie: cookie})
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
	vs, err := e.svc.ListVersions(t.Context(), e.site.ID)
	if err != nil {
		t.Fatal(err)
	}
	v1 := vs[0].ID
	e.publish(e.site.ID, sites.Merge, map[string]string{"index.html": "<h1>home v2</h1>", "new.html": "new"})
	if get("/") != "<h1>home v2</h1>" || get("/new") != "new" {
		t.Fatal("v2 not served")
	}
	if err := e.svc.Rollback(t.Context(), e.site.ID, v1); err != nil {
		t.Fatal(err)
	}
	if get("/") != "<h1>home</h1>" || get("/new") != "<h1>custom 404</h1>" {
		t.Fatalf("after rollback: %q %q", get("/"), get("/new"))
	}
}

// With an https APP_ORIGIN, site hosts (same scheme) send HSTS too.
func TestSiteHSTS(t *testing.T) {
	for _, scheme := range []string{"https", "http"} {
		d := sitestest.Deps(t)
		origin := *d.Config.AppOrigin
		origin.Scheme = scheme
		cfg := *d.Config
		cfg.AppOrigin = &origin
		d2 := *d
		d2.Config = &cfg
		h, err := server.New(&d2, siteserver.Register)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", scheme+"://"+siteHost("abcdefghijkl")+"/_auth/share", nil))
		got := rec.Header().Get("Strict-Transport-Security")
		if (scheme == "https") != (got == "max-age=31536000") || (scheme == "http" && got != "") {
			t.Errorf("%s: HSTS %q", scheme, got)
		}
	}
}

func TestAuthorized(t *testing.T) {
	user := uuid.New()
	member := token.SiteCookie{SiteID: "aaaaaaaaaaaa", UserID: user}
	public := token.SiteCookie{SiteID: "aaaaaaaaaaaa", LinkGen: 3}
	for _, tc := range []struct {
		c    token.SiteCookie
		a    sites.Access
		want bool
	}{
		{member, sites.Access{SiteID: "aaaaaaaaaaaa", Role: "viewer"}, true},
		{member, sites.Access{SiteID: "aaaaaaaaaaaa", Role: "owner"}, true},
		{member, sites.Access{SiteID: "aaaaaaaaaaaa"}, false},
		{member, sites.Access{SiteID: "bbbbbbbbbbbb", Role: "owner"}, false},
		{member, sites.Access{SiteID: "aaaaaaaaaaaa", PublicLinkEnabled: true, PublicLinkGen: 0}, false},
		{public, sites.Access{SiteID: "aaaaaaaaaaaa", PublicLinkEnabled: true, PublicLinkGen: 3}, true},
		{public, sites.Access{SiteID: "aaaaaaaaaaaa", PublicLinkEnabled: true, PublicLinkGen: 4}, false},
		{public, sites.Access{SiteID: "aaaaaaaaaaaa", PublicLinkEnabled: false, PublicLinkGen: 3}, false},
		{token.SiteCookie{SiteID: "aaaaaaaaaaaa"}, sites.Access{SiteID: "aaaaaaaaaaaa", PublicLinkEnabled: true}, false},
	} {
		if got := siteserver.Authorized(tc.c, tc.a); got != tc.want {
			t.Errorf("Authorized(%+v, %+v) = %v", tc.c, tc.a, got)
		}
	}
}
