package appapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/server"
)

const appOrigin = "http://dash.localhost:8083"

type testEnv struct {
	h    http.Handler
	deps *server.Deps
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			"APP_ORIGIN":   appOrigin,
			"SITES_DOMAIN": "sites.localhost",
			"SECRET_KEY":   strings.Repeat("k", 32),
			"DATABASE_URL": "postgres://localhost/x",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	d := &server.Deps{Config: cfg, Logger: slog.New(slog.DiscardHandler)}
	h, err := server.New(d, Register, func(r *server.Routes, d *server.Deps) error {
		// Stand-in for the session middleware: "X-Test-User: 1" signs in.
		r.UseApp(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("X-Test-User") != "" {
					req = req.WithContext(identity.WithUser(req.Context(), identity.User{ID: uuid.New()}))
				}
				next.ServeHTTP(w, req)
			})
		})
		Handle(r, d, "GET /app-api/csrf", func(w http.ResponseWriter, r *http.Request) error {
			WriteJSON(w, 200, map[string]string{"csrfToken": CSRFToken(w, r, d)})
			return nil
		}, Public())
		Handle(r, d, "POST /app-api/echo", func(w http.ResponseWriter, r *http.Request) error {
			var in struct {
				Name string `json:"name"`
			}
			if err := ReadJSON(r, &in); err != nil {
				return err
			}
			WriteJSON(w, 200, in)
			return nil
		})
		Handle(r, d, "POST /app-api/public", func(w http.ResponseWriter, r *http.Request) error {
			WriteJSON(w, 200, map[string]bool{"ok": true})
			return nil
		}, Public())
		Handle(r, d, "GET /app-api/fail", func(w http.ResponseWriter, r *http.Request) error {
			return io.ErrUnexpectedEOF
		})
		r.App.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ui") })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{h: h, deps: d}
}

// csrf returns a CSRF cookie and its token from GET /app-api/csrf.
func (e *testEnv) csrf(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	rec := e.do(httptest.NewRequest("GET", appOrigin+"/app-api/csrf", nil))
	var out struct{ CSRFToken string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.CSRFToken == "" {
		t.Fatalf("csrf: %v %q", err, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CSRFCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/" {
		t.Fatalf("csrf cookie = %+v", cookies)
	}
	return cookies[0], out.CSRFToken
}

func (e *testEnv) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, body %q", ct, rec.Body.String())
	}
	var out struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out.Error.Code
}

func TestOriginAndCSRF(t *testing.T) {
	e := newEnv(t)
	cookie, tok := e.csrf(t)
	_, otherTok := e.csrf(t)

	tests := []struct {
		name          string
		origin, token string
		cookie        bool
		status        int
		code          string
	}{
		{"valid", appOrigin, tok, true, 200, ""},
		{"missing origin", "", tok, true, 403, "bad_origin"},
		{"wrong origin", "http://evil.example", tok, true, 403, "bad_origin"},
		{"site host origin", "http://abc.sites.localhost:8083", tok, true, 403, "bad_origin"},
		{"site id origin", "http://abcdefghijkl.sites.localhost:8083", tok, true, 403, "bad_origin"},
		{"origin without port", "http://dash.localhost", tok, true, 403, "bad_origin"},
		{"https origin", "https://dash.localhost:8083", tok, true, 403, "bad_origin"},
		{"null origin", "null", tok, true, 403, "bad_origin"},
		{"missing token", appOrigin, "", true, 403, "bad_csrf"},
		{"wrong token", appOrigin, "AAAA", true, 403, "bad_csrf"},
		{"token of another cookie", appOrigin, otherTok, true, 403, "bad_csrf"},
		{"missing cookie", appOrigin, tok, false, 403, "bad_csrf"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/app-api/echo", "/app-api/public"} {
				req := httptest.NewRequest("POST", appOrigin+path, strings.NewReader(`{"name":"x"}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Test-User", "1")
				if tc.origin != "" {
					req.Header.Set("Origin", tc.origin)
				}
				if tc.token != "" {
					req.Header.Set(CSRFHeader, tc.token)
				}
				if tc.cookie {
					req.AddCookie(cookie)
				}
				rec := e.do(req)
				if rec.Code != tc.status {
					t.Fatalf("%s: status %d, want %d (%s)", path, rec.Code, tc.status, rec.Body.String())
				}
				if tc.code != "" {
					if got := errorCode(t, rec); got != tc.code {
						t.Fatalf("%s: code %q, want %q", path, got, tc.code)
					}
				}
			}
		})
	}
}

func TestSessionRequired(t *testing.T) {
	e := newEnv(t)
	cookie, tok := e.csrf(t)
	post := func(signedIn bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", appOrigin+"/app-api/echo", strings.NewReader(`{"name":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", appOrigin)
		req.Header.Set(CSRFHeader, tok)
		req.AddCookie(cookie)
		if signedIn {
			req.Header.Set("X-Test-User", "1")
		}
		return e.do(req)
	}
	if rec := post(false); rec.Code != 401 || errorCode(t, rec) != "unauthenticated" {
		t.Fatalf("signed out: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(true); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"name":"x"}` {
		t.Fatalf("signed in: %d %s", rec.Code, rec.Body.String())
	}
	// Safe methods need no CSRF token but still a session.
	req := httptest.NewRequest("GET", appOrigin+"/app-api/fail", nil)
	if rec := e.do(req); rec.Code != 401 {
		t.Fatalf("GET signed out: %d", rec.Code)
	}
}

func TestErrorsAndNotFound(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", appOrigin+"/app-api/fail", nil)
	req.Header.Set("X-Test-User", "1")
	if rec := e.do(req); rec.Code != 500 || errorCode(t, rec) != "internal" {
		t.Fatalf("internal error: %d %s", rec.Code, rec.Body.String())
	}
	for _, m := range []string{"GET", "POST", "DELETE", "HEAD"} {
		rec := e.do(httptest.NewRequest(m, appOrigin+"/app-api/nope/x", nil))
		if rec.Code != 404 {
			t.Fatalf("%s unknown endpoint: %d", m, rec.Code)
		}
		if m != "HEAD" && errorCode(t, rec) != "not_found" {
			t.Fatalf("%s unknown endpoint body %q", m, rec.Body.String())
		}
	}
	if rec := e.do(httptest.NewRequest("GET", appOrigin+"/elsewhere", nil)); rec.Body.String() != "ui" {
		t.Fatalf("UI route shadowed: %q", rec.Body.String())
	}
}

func TestReadJSON(t *testing.T) {
	e := newEnv(t)
	cookie, tok := e.csrf(t)
	post := func(ct, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", appOrigin+"/app-api/echo", strings.NewReader(body))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Origin", appOrigin)
		req.Header.Set(CSRFHeader, tok)
		req.Header.Set("X-Test-User", "1")
		req.AddCookie(cookie)
		return e.do(req)
	}
	tests := []struct {
		ct, body string
		status   int
		code     string
	}{
		{"application/json; charset=utf-8", `{"name":"a"}`, 200, ""},
		{"text/plain", `{"name":"a"}`, 415, "unsupported_media_type"},
		{"application/x-www-form-urlencoded", `name=a`, 415, "unsupported_media_type"},
		{"application/json", `{"name":"a","extra":1}`, 400, "invalid_json"},
		{"application/json", `{"name":"a"} {}`, 400, "invalid_json"},
		{"application/json", `{"name":`, 400, "invalid_json"},
		{"application/json", `{"name":"` + strings.Repeat("a", int(config.DefaultLimits().MaxJSONBody)) + `"}`, 413, "too_large"},
	}
	for _, tc := range tests {
		rec := post(tc.ct, tc.body)
		if rec.Code != tc.status {
			t.Fatalf("%s %.40q: status %d, want %d", tc.ct, tc.body, rec.Code, tc.status)
		}
		if tc.code != "" && errorCode(t, rec) != tc.code {
			t.Fatalf("%s %.40q: wrong code %s", tc.ct, tc.body, rec.Body.String())
		}
	}
}
