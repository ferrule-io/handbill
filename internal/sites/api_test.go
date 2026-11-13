package sites_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
)

type apiEnv struct {
	*env
	h          http.Handler
	csrfCookie *http.Cookie
	csrfToken  string
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := newEnv(t)
	h, err := server.New(e.d, appapi.Register, sites.Register, func(r *server.Routes, d *server.Deps) error {
		// Stand-in for the session middleware.
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
	a := &apiEnv{env: e, h: h}
	rec := a.call(t, uuid.Nil, "GET", "/app-api/test-csrf", "")
	var out struct{ CsrfToken string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	a.csrfToken = out.CsrfToken
	for _, c := range rec.Result().Cookies() {
		if c.Name == appapi.CSRFCookie {
			a.csrfCookie = c
		}
	}
	if a.csrfCookie == nil || a.csrfToken == "" {
		t.Fatal("no CSRF token")
	}
	return a
}

func (a *apiEnv) call(t *testing.T, user uuid.UUID, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
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

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return v
}

type siteResp struct {
	ID, Title, Description, URL, Role string
	CurrentVersionID                  *string
}

type versionResp struct {
	ID          string
	PublishedBy *string
	FileCount   int
	Current     bool
}

func TestAPI(t *testing.T) {
	a := newAPIEnv(t)
	s := a.site(t)
	v1, err := a.svc.Publish(t.Context(), s.ID, a.owner, sites.Replace, []sites.File{file("index.html", "one")}, nil)
	must(t, err)
	v2, err := a.svc.Publish(t.Context(), s.ID, a.owner, sites.Replace, []sites.File{file("index.html", "two"), file("b.css", "b")}, nil)
	must(t, err)
	manager := sitestest.User(t, a.d, "manager@example.com")
	viewer := sitestest.User(t, a.d, "viewer@example.com")
	stranger := sitestest.User(t, a.d, "stranger@example.com")
	sitestest.Member(t, a.d, s.ID, manager, "manager")
	sitestest.Member(t, a.d, s.ID, viewer, "viewer")

	// Signed out: 401.
	if rec := a.call(t, uuid.Nil, "GET", "/app-api/sites", ""); rec.Code != 401 {
		t.Fatalf("signed out list: %d", rec.Code)
	}

	// List shows the caller's sites with role and URL.
	rec := a.call(t, viewer, "GET", "/app-api/sites", "")
	list := decode[struct{ Sites []siteResp }](t, rec)
	if rec.Code != 200 || len(list.Sites) != 1 || list.Sites[0].Role != "viewer" ||
		list.Sites[0].URL != "http://"+s.ID+".sites.localhost:8084/" || *list.Sites[0].CurrentVersionID != v2.ID.String() {
		t.Fatalf("viewer list %d %+v", rec.Code, list)
	}
	if got := decode[struct{ Sites []siteResp }](t, a.call(t, stranger, "GET", "/app-api/sites", "")); len(got.Sites) != 0 {
		t.Fatalf("stranger list %+v", got)
	}

	// Get: members see it; non-members get the same 404 as a missing site.
	rec = a.call(t, viewer, "GET", "/app-api/sites/"+s.ID, "")
	got := decode[struct {
		Site           siteResp
		CurrentVersion versionResp
	}](t, rec)
	if rec.Code != 200 || got.Site.Title != "Trip options" || got.CurrentVersion.ID != v2.ID.String() || got.CurrentVersion.FileCount != 2 ||
		*got.CurrentVersion.PublishedBy != "owner@example.com" || !got.CurrentVersion.Current {
		t.Fatalf("get %d %+v", rec.Code, got)
	}
	r1 := a.call(t, stranger, "GET", "/app-api/sites/"+s.ID, "")
	r2 := a.call(t, stranger, "GET", "/app-api/sites/zzzzzzzzzzzz", "")
	if r1.Code != 404 || r1.Body.String() != r2.Body.String() {
		t.Fatalf("stranger get %d %q vs missing %q", r1.Code, r1.Body, r2.Body)
	}

	// Update: managers and owners; viewers are forbidden.
	if rec := a.call(t, viewer, "PATCH", "/app-api/sites/"+s.ID, `{"title":"x"}`); rec.Code != 403 {
		t.Fatalf("viewer update %d", rec.Code)
	}
	if rec := a.call(t, stranger, "PATCH", "/app-api/sites/"+s.ID, `{"title":"x"}`); rec.Code != 404 {
		t.Fatalf("stranger update %d", rec.Code)
	}
	rec = a.call(t, manager, "PATCH", "/app-api/sites/"+s.ID, `{"description":"Picked one"}`)
	if up := decode[struct{ Site siteResp }](t, rec); rec.Code != 200 || up.Site.Description != "Picked one" || up.Site.Title != "Trip options" {
		t.Fatalf("manager update %d %q", rec.Code, rec.Body)
	}
	if rec := a.call(t, manager, "PATCH", "/app-api/sites/"+s.ID, `{"title":""}`); rec.Code != 400 {
		t.Fatalf("empty title %d", rec.Code)
	}

	// Versions and rollback: managers and owners only.
	if rec := a.call(t, viewer, "GET", "/app-api/sites/"+s.ID+"/versions", ""); rec.Code != 403 {
		t.Fatalf("viewer versions %d", rec.Code)
	}
	rec = a.call(t, manager, "GET", "/app-api/sites/"+s.ID+"/versions", "")
	vs := decode[struct{ Versions []versionResp }](t, rec)
	if rec.Code != 200 || len(vs.Versions) != 2 || vs.Versions[0].ID != v2.ID.String() || !vs.Versions[0].Current || vs.Versions[1].Current {
		t.Fatalf("versions %d %+v", rec.Code, vs)
	}
	body := `{"versionId":"` + v1.ID.String() + `"}`
	if rec := a.call(t, viewer, "POST", "/app-api/sites/"+s.ID+"/rollback", body); rec.Code != 403 {
		t.Fatalf("viewer rollback %d", rec.Code)
	}
	if rec := a.call(t, manager, "POST", "/app-api/sites/"+s.ID+"/rollback", `{"versionId":"`+uuid.NewString()+`"}`); rec.Code != 404 {
		t.Fatalf("rollback to unknown version %d", rec.Code)
	}
	rec = a.call(t, manager, "POST", "/app-api/sites/"+s.ID+"/rollback", body)
	if rb := decode[struct{ CurrentVersion versionResp }](t, rec); rec.Code != 200 || rb.CurrentVersion.ID != v1.ID.String() {
		t.Fatalf("rollback %d %q", rec.Code, rec.Body)
	}
	wantContents(t, a.contents(t, s.ID), map[string]string{"index.html": "one"})

	// Unsafe requests without CSRF are refused.
	csrf := a.csrfCookie
	a.csrfCookie = nil
	if rec := a.call(t, a.owner, "DELETE", "/app-api/sites/"+s.ID, ""); rec.Code != 403 {
		t.Fatalf("delete without CSRF %d", rec.Code)
	}
	a.csrfCookie = csrf

	// Delete: owner only.
	if rec := a.call(t, manager, "DELETE", "/app-api/sites/"+s.ID, ""); rec.Code != 403 {
		t.Fatalf("manager delete %d", rec.Code)
	}
	if rec := a.call(t, a.owner, "DELETE", "/app-api/sites/"+s.ID, ""); rec.Code != 200 {
		t.Fatalf("owner delete %d %q", rec.Code, rec.Body)
	}
	if rec := a.call(t, a.owner, "GET", "/app-api/sites/"+s.ID, ""); rec.Code != 404 {
		t.Fatalf("get after delete %d", rec.Code)
	}
}
