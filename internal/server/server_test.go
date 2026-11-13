package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mjquinlan2000/agent-dash/internal/config"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			"APP_ORIGIN":   "http://dash.localhost:8080",
			"SITES_DOMAIN": "sites.localhost",
			"SECRET_KEY":   strings.Repeat("x", 32),
			"DATABASE_URL": "postgres://localhost/x",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{Config: cfg, Logger: slog.New(slog.DiscardHandler)}
	h, err := New(d, func(r *Routes, d *Deps) error {
		r.App.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "app "+r.URL.Path)
		})
		r.Sites.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "site "+SiteID(r.Context())+" "+r.URL.Path)
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHostRouting(t *testing.T) {
	h := testHandler(t)
	tests := []struct {
		host, path string
		status     int
		body       string
	}{
		{"dash.localhost:8080", "/site", 200, "app /site"},
		{"DASH.localhost", "/", 200, "app /"},
		{"dash.localhost.:8080", "/x", 200, "app /x"},
		{"abcdefghijkl.sites.localhost:8080", "/index.html", 200, "site abcdefghijkl /index.html"},
		{"ABCDEFGHIJKL.sites.localhost", "/", 200, "site abcdefghijkl /"},
		{"abcdefghijk.sites.localhost", "/", 404, ""},    // 11 chars
		{"abcdefghijk1.sites.localhost", "/", 404, ""},   // not base32
		{"a.abcdefghijkl.sites.localhost", "/", 404, ""}, // two labels
		{"sites.localhost", "/", 404, ""},
		{"example.com", "/", 404, ""},
		{"10.0.0.7:8080", "/", 404, ""},
		{"10.0.0.7:8080", "/healthz", 200, "ok\n"},
		{"abcdefghijkl.sites.localhost", "/healthz", 200, "ok\n"},
		{"dash.localhost", "/readyz", 200, "ok\n"},
	}
	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		req.Host = tt.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.status {
			t.Errorf("%s%s: status %d, want %d", tt.host, tt.path, rec.Code, tt.status)
			continue
		}
		if tt.body != "" && rec.Body.String() != tt.body {
			t.Errorf("%s%s: body %q, want %q", tt.host, tt.path, rec.Body, tt.body)
		}
	}
}

func TestProbeMethods(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest("POST", "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz: %d", rec.Code)
	}
}

func TestLogPath(t *testing.T) {
	for in, want := range map[string]string{
		"/site":                  "/site",
		"/_auth/callback":        "/_auth/callback",
		"/_auth/share/secrettok": "/_auth/share/…",
	} {
		if got := LogPath(in); got != want {
			t.Errorf("LogPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMiddlewareScopedToHost(t *testing.T) {
	cfg, _ := config.Load(func(k string) string {
		return map[string]string{
			"APP_ORIGIN":   "https://dash.example.com",
			"SITES_DOMAIN": "sites.example.com",
			"SECRET_KEY":   strings.Repeat("x", 32),
			"DATABASE_URL": "postgres://localhost/x",
		}[k]
	})
	tag := func(s string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Chain", s)
				next.ServeHTTP(w, r)
			})
		}
	}
	h, err := New(&Deps{Config: cfg, Logger: slog.New(slog.DiscardHandler)}, func(r *Routes, d *Deps) error {
		r.UseApp(tag("app1"))
		r.UseApp(tag("app2"))
		r.UseSites(tag("site"))
		r.App.HandleFunc("GET /", func(http.ResponseWriter, *http.Request) {})
		r.Sites.HandleFunc("GET /", func(http.ResponseWriter, *http.Request) {})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{
		"dash.example.com":               "app1,app2",
		"abcdefghijkl.sites.example.com": "site",
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := strings.Join(rec.Header().Values("X-Chain"), ","); got != want {
			t.Errorf("%s: chain %q, want %q", host, got, want)
		}
	}
}
