package sharing_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sharing"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/siteserver"
	"github.com/ferrule-io/handbill/internal/token"
)

// httpEnv serves the app API (sites + sharing) and the site host over the
// test database, with a stand-in session middleware (X-Test-User).
type httpEnv struct {
	*env
	h          http.Handler
	csrfCookie *http.Cookie
	csrfToken  string
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	e := newEnv(t)
	h, err := server.New(e.d, appapi.Register, sites.Register, sharing.Register, siteserver.Register, func(r *server.Routes, d *server.Deps) error {
		r.UseApp(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if id, err := uuid.Parse(req.Header.Get("X-Test-User")); err == nil {
					req = req.WithContext(identity.WithUser(req.Context(), identity.User{ID: id}))
				}
				next.ServeHTTP(w, req)
			})
		})
		appapi.Handle(r, d, "GET /app-api/test-csrf", func(w http.ResponseWriter, r *http.Request) error {
			appapi.WriteJSON(w, 200, map[string]string{"csrfToken": appapi.CSRFToken(w, r, d)})
			return nil
		}, appapi.Public())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &httpEnv{env: e, h: h}
	rec := a.call(uuid.Nil, "GET", "/app-api/test-csrf", "")
	var out struct{ CsrfToken string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	a.csrfToken = out.CsrfToken
	for _, c := range rec.Result().Cookies() {
		if c.Name == appapi.CSRFCookie {
			a.csrfCookie = c
		}
	}
	if a.csrfCookie == nil {
		t.Fatal("no CSRF cookie")
	}
	return a
}

func (a *httpEnv) call(user uuid.UUID, method, path, body string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, sitestest.AppOrigin+path, strings.NewReader(body))
	if user != uuid.Nil {
		req.Header.Set("X-Test-User", user.String())
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" && a.csrfCookie != nil {
		req.Header.Set("Origin", sitestest.AppOrigin)
		req.Header.Set(appapi.CSRFHeader, a.csrfToken)
		req.AddCookie(a.csrfCookie)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

// get fetches an absolute URL (a site host) with an optional cookie.
func (a *httpEnv) get(rawURL string, c *http.Cookie) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest("GET", rawURL, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

// redeem posts a public-link token to a site host's /_auth/share, as the
// opener page's script does.
func (a *httpEnv) redeem(site, tok string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest("POST", siteURL(site, "/_auth/share"), strings.NewReader(url.Values{"token": {tok}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

// open follows a public link like a browser: the token is in the fragment,
// the server sees only GET /_auth/share (the opener page), and the page
// posts the token.
func (a *httpEnv) open(link string) *httptest.ResponseRecorder {
	a.t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Path != "/_auth/share" || u.RawQuery != "" {
		a.t.Fatalf("public link %q is not <site>/_auth/share#token=…", link)
	}
	frag, _ := url.ParseQuery(u.Fragment)
	opener := a.get(u.Scheme+"://"+u.Host+u.Path, nil)
	if opener.Code != http.StatusOK || !strings.Contains(opener.Body.String(), `action="/_auth/share"`) {
		a.t.Fatalf("opener page: %d %q", opener.Code, opener.Body)
	}
	return a.redeem(strings.Split(u.Hostname(), ".")[0], frag.Get("token"))
}

func siteURL(id, path string) string {
	return "http://" + id + "." + sitestest.SitesDomain + ":8084" + path
}

func (a *httpEnv) publish(siteID, body string) {
	a.t.Helper()
	if _, err := a.sites.Publish(a.t.Context(), siteID, a.owner, sites.Replace, []sites.File{{Path: "index.html", Body: []byte(body)}}, nil); err != nil {
		a.t.Fatal(err)
	}
}

func siteCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == siteserver.CookieName {
			return c
		}
	}
	t.Fatalf("no site cookie (status %d)", rec.Code)
	return nil
}

func wantRedirectToHandoff(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), sitestest.AppOrigin+"/auth/site?") {
		t.Fatalf("status %d location %q, want the sign-in handoff", rec.Code, rec.Header().Get("Location"))
	}
}

func TestShareRoute(t *testing.T) {
	a := newHTTPEnv(t)
	ctx := t.Context()
	site := a.site()
	a.publish(site, "<h1>public</h1>")
	link, err := a.svc.SetPublicLink(ctx, site, a.owner, sharing.LinkEnable)
	wantErr(t, err, nil)

	// Without a cookie the site sends visitors to sign in.
	wantRedirectToHandoff(t, a.get(siteURL(site, "/"), nil))

	rec := a.open(link)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("share: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("share headers: %v", rec.Header())
	}
	c := siteCookie(t, rec)
	if !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes: %+v", c)
	}
	sc, err := token.NewSigner(a.d.Config.SecretKey).ParseSiteCookie(c.Value, time.Now())
	wantErr(t, err, nil)
	if sc.UserID != uuid.Nil || sc.LinkGen != 1 || sc.SiteID != site {
		t.Fatalf("cookie = %+v", sc)
	}
	if rec := a.get(siteURL(site, "/"), c); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "public") {
		t.Fatalf("with public cookie: %d %q", rec.Code, rec.Body)
	}

	// Rotate: the old cookie and the old link stop working on the next request.
	rotated, err := a.svc.SetPublicLink(ctx, site, a.owner, sharing.LinkRotate)
	wantErr(t, err, nil)
	wantRedirectToHandoff(t, a.get(siteURL(site, "/"), c))
	if rec := a.open(link); rec.Code != http.StatusNotFound || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("old link: %d", rec.Code)
	}
	c2 := siteCookie(t, a.open(rotated))
	if rec := a.get(siteURL(site, "/"), c2); rec.Code != http.StatusOK {
		t.Fatalf("rotated cookie: %d", rec.Code)
	}

	// Disable: the current cookie and link are refused too.
	_, err = a.svc.SetPublicLink(ctx, site, a.owner, sharing.LinkDisable)
	wantErr(t, err, nil)
	wantRedirectToHandoff(t, a.get(siteURL(site, "/"), c2))
	if rec := a.open(rotated); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled link: %d", rec.Code)
	}
	// Re-enabling issues a new generation: earlier cookies stay refused.
	again, err := a.svc.SetPublicLink(ctx, site, a.owner, sharing.LinkEnable)
	wantErr(t, err, nil)
	wantRedirectToHandoff(t, a.get(siteURL(site, "/"), c2))
	if rec := a.get(siteURL(site, "/"), siteCookie(t, a.open(again))); rec.Code != http.StatusOK {
		t.Fatalf("re-enabled: %d", rec.Code)
	}

	// Another site's token is refused on this host, neutrally.
	other := a.site()
	a.publish(other, "<h1>other</h1>")
	otherLink, err := a.svc.SetPublicLink(ctx, other, a.owner, sharing.LinkEnable)
	wantErr(t, err, nil)
	tok := tokenOf(t, otherLink)
	rec = a.redeem(site, tok)
	if rec.Code != http.StatusNotFound || len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), "isn’t active") {
		t.Fatalf("cross-site token: %d %q", rec.Code, rec.Body)
	}
	for _, tok := range []string{"garbage", ""} {
		if rec := a.redeem(site, tok); rec.Code != http.StatusNotFound || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	// The token never travels in a path: the old /_auth/share/<token> form
	// is just a reserved path.
	if rec := a.get(siteURL(other, "/_auth/share/"+tok), nil); rec.Code != http.StatusNotFound || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("token in path: %d", rec.Code)
	}
	// A public cookie for one site is no good on another.
	if rec := a.get(siteURL(site, "/"), siteCookie(t, a.open(otherLink))); rec.Code != http.StatusFound {
		t.Fatalf("other site's cookie: %d", rec.Code)
	}
}

func TestRevocationTakesEffectNextRequest(t *testing.T) {
	a := newHTTPEnv(t)
	ctx := t.Context()
	site := a.site()
	a.publish(site, "<h1>members only</h1>")
	viewer := a.member(site, rbac.Viewer)
	c := &http.Cookie{Name: siteserver.CookieName, Value: token.NewSigner(a.d.Config.SecretKey).SiteCookie(
		token.SiteCookie{SiteID: site, UserID: viewer, Expires: time.Now().Add(time.Hour)})}
	if rec := a.get(siteURL(site, "/"), c); rec.Code != http.StatusOK {
		t.Fatalf("member: %d", rec.Code)
	}
	if rec := a.call(viewer, "GET", "/app-api/sites/"+site, ""); rec.Code != http.StatusOK {
		t.Fatalf("app api: %d", rec.Code)
	}
	wantErr(t, a.svc.Revoke(ctx, site, a.owner, viewer), nil)
	wantRedirectToHandoff(t, a.get(siteURL(site, "/"), c))
	if rec := a.call(viewer, "GET", "/app-api/sites/"+site, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("app api after revoke: %d", rec.Code)
	}

	// Role changes too: a demoted manager loses manager endpoints at once.
	mgr := a.member(site, rbac.Manager)
	if rec := a.call(mgr, "GET", "/app-api/sites/"+site+"/members", ""); rec.Code != http.StatusOK {
		t.Fatalf("manager members: %d", rec.Code)
	}
	wantErr(t, a.svc.SetRole(ctx, site, a.owner, mgr, rbac.Viewer), nil)
	if rec := a.call(mgr, "GET", "/app-api/sites/"+site+"/members", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("demoted manager members: %d", rec.Code)
	}
}

// TestRolesTableOverHTTP checks every row of the design's roles table
// through the UI's endpoints, for each role and for a non-member.
func TestRolesTableOverHTTP(t *testing.T) {
	a := newHTTPEnv(t)
	type fixture struct {
		site            string
		viewer, manager uuid.UUID
		version         string
	}
	setup := func() fixture {
		f := fixture{site: a.site()}
		a.publish(f.site, "<h1>v1</h1>")
		v, err := a.sites.Publish(a.t.Context(), f.site, a.owner, sites.Replace, []sites.File{{Path: "index.html", Body: []byte("v2")}}, nil)
		wantErr(t, err, nil)
		vs, _ := a.sites.ListVersions(a.t.Context(), f.site)
		for _, x := range vs {
			if x.ID != v.ID {
				f.version = x.ID.String()
			}
		}
		f.viewer = a.member(f.site, rbac.Viewer)
		f.manager = a.member(f.site, rbac.Manager)
		return f
	}
	rows := []struct {
		name   string
		action rbac.Action
		method string
		path   func(f fixture) string
		body   func(f fixture) string
	}{
		{"view site", rbac.View, "GET", func(f fixture) string { return "/app-api/sites/" + f.site }, nil},
		{"list versions", rbac.Publish, "GET", func(f fixture) string { return "/app-api/sites/" + f.site + "/versions" }, nil},
		{"roll back", rbac.Publish, "POST", func(f fixture) string { return "/app-api/sites/" + f.site + "/rollback" },
			func(f fixture) string { return `{"versionId":"` + f.version + `"}` }},
		{"edit details", rbac.EditMeta, "PATCH", func(f fixture) string { return "/app-api/sites/" + f.site },
			func(fixture) string { return `{"title":"Renamed"}` }},
		{"list members", rbac.ManageViewers, "GET", func(f fixture) string { return "/app-api/sites/" + f.site + "/members" }, nil},
		{"invite viewer", rbac.ManageViewers, "POST", func(f fixture) string { return "/app-api/sites/" + f.site + "/members" },
			func(fixture) string { return `{"email":"v@example.org","role":"viewer","notify":false}` }},
		{"remove viewer", rbac.ManageViewers, "DELETE", func(f fixture) string { return "/app-api/sites/" + f.site + "/members/" + f.viewer.String() }, nil},
		{"invite manager", rbac.ManageManagers, "POST", func(f fixture) string { return "/app-api/sites/" + f.site + "/members" },
			func(fixture) string { return `{"email":"m@example.org","role":"manager","notify":false}` }},
		{"remove manager", rbac.ManageManagers, "DELETE", func(f fixture) string { return "/app-api/sites/" + f.site + "/members/" + f.manager.String() }, nil},
		{"promote viewer", rbac.ManageManagers, "PATCH", func(f fixture) string { return "/app-api/sites/" + f.site + "/members/" + f.viewer.String() },
			func(fixture) string { return `{"role":"manager"}` }},
		{"public link", rbac.ManagePublicLink, "POST", func(f fixture) string { return "/app-api/sites/" + f.site + "/public-link" },
			func(fixture) string { return `{"op":"enable"}` }},
		{"transfer", rbac.Transfer, "POST", func(f fixture) string { return "/app-api/sites/" + f.site + "/transfer" },
			func(fixture) string { return `{"email":"heir@example.org","notify":false}` }},
		{"delete site", rbac.Delete, "DELETE", func(f fixture) string { return "/app-api/sites/" + f.site }, nil},
	}
	for _, role := range []rbac.Role{rbac.Owner, rbac.Manager, rbac.Viewer, ""} {
		for _, row := range rows {
			name := string(role)
			if name == "" {
				name = "stranger"
			}
			t.Run(name+"/"+row.name, func(t *testing.T) {
				f := setup()
				var by uuid.UUID
				switch role {
				case rbac.Owner:
					by = a.owner
				case rbac.Manager:
					by = f.manager
				case rbac.Viewer:
					by = f.viewer
				default:
					by = a.user("stranger")
				}
				body := ""
				if row.body != nil {
					body = row.body(f)
				}
				rec := a.call(by, row.method, row.path(f), body)
				want := http.StatusOK
				switch {
				case role == "":
					want = http.StatusNotFound
				case !rbac.Can(role, row.action):
					want = http.StatusForbidden
				}
				if rec.Code != want {
					t.Fatalf("%s %s as %s: %d, want %d; %s", row.method, row.path(f), name, rec.Code, want, rec.Body)
				}
			})
		}
	}
}

func TestInvitePageEndpoints(t *testing.T) {
	a := newHTTPEnv(t)
	site := a.site()
	rec := a.call(a.owner, "POST", "/app-api/sites/"+site+"/members", `{"email":"Pal@Example.com","role":"viewer","notify":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	var inv struct {
		Link               string
		Granted            bool
		Emailed            bool
		EmailNotSentReason *string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &inv)
	// No SMTP in this server: the link is returned, nothing is sent.
	if inv.Granted || inv.Emailed || !strings.Contains(inv.Link, "/invite#token=") ||
		inv.EmailNotSentReason == nil || *inv.EmailNotSentReason != "mail_disabled" {
		t.Fatalf("invite = %s", rec.Body)
	}
	tok := tokenOf(t, inv.Link)

	// The member list shows the invite but never its token, and whether
	// each member's address is verified.
	rec = a.call(a.owner, "GET", "/app-api/sites/"+site+"/members", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), tok) || !strings.Contains(rec.Body.String(), "pal@example.com") {
		t.Fatalf("members: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Members []struct{ EmailVerified *bool }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Members) != 1 || list.Members[0].EmailVerified == nil || !*list.Members[0].EmailVerified {
		t.Fatalf("members emailVerified: %s", rec.Body)
	}

	// Signed out: lookup works, accept needs a session.
	rec = a.call(uuid.Nil, "POST", "/app-api/invites/lookup", `{"token":"`+tok+`"}`)
	var p struct {
		Kind, Status string
		Site         struct{ ID, Title string }
		Role         string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != http.StatusOK || p.Kind != "invite" || p.Status != "pending" || p.Site.Title != "Kyoto in April" || p.Role != "viewer" {
		t.Fatalf("lookup: %d %s", rec.Code, rec.Body)
	}
	if rec := a.call(uuid.Nil, "POST", "/app-api/invites/accept", `{"token":"`+tok+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out accept: %d", rec.Code)
	}
	if rec := a.call(uuid.Nil, "POST", "/app-api/invites/lookup", `{"token":"nope"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown lookup: %d", rec.Code)
	}

	u := a.user("pal")
	rec = a.call(u, "POST", "/app-api/invites/accept", `{"token":"`+tok+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), site) {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	if rec := a.call(a.user("late"), "POST", "/app-api/invites/accept", `{"token":"`+tok+`"}`); rec.Code != http.StatusGone {
		t.Fatalf("second accept: %d", rec.Code)
	}
	// CSRF/Origin apply to these endpoints like every other.
	req := httptest.NewRequest("POST", sitestest.AppOrigin+"/app-api/invites/accept", strings.NewReader(`{"token":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", u.String())
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin accept: %d", w.Code)
	}

	// Public link and transfer state never include a link.
	rec = a.call(a.owner, "POST", "/app-api/sites/"+site+"/public-link", `{"op":"enable"}`)
	var pl struct {
		Enabled bool
		Link    *string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pl)
	if rec.Code != http.StatusOK || !pl.Enabled || pl.Link == nil {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	rec = a.call(a.owner, "GET", "/app-api/sites/"+site+"/public-link", "")
	if strings.Contains(rec.Body.String(), tokenOf(t, *pl.Link)) || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("public link state: %s", rec.Body)
	}
	rec = a.call(a.owner, "POST", "/app-api/sites/"+site+"/public-link", `{"op":"enable"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double enable: %d", rec.Code)
	}
	rec = a.call(a.owner, "POST", "/app-api/sites/"+site+"/transfer", `{"email":"heir@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("transfer: %d %s", rec.Code, rec.Body)
	}
	rec = a.call(a.owner, "GET", "/app-api/sites/"+site+"/transfer", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "heir@example.com") || strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("transfer state: %s", rec.Body)
	}
	if rec := a.call(a.owner, "DELETE", "/app-api/sites/"+site+"/transfer", ""); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d", rec.Code)
	}
	if rec := a.call(a.owner, "GET", "/app-api/sites/"+site+"/transfer", ""); !strings.Contains(rec.Body.String(), `"transfer":null`) {
		t.Fatalf("after cancel: %s", rec.Body)
	}
}
