package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/server"
)

func testFS() fstest.MapFS {
	f := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	return fstest.MapFS{
		"index.html":                 f("home"),
		"404.html":                   f("not found page"),
		"site.html":                  f("site page"),
		"site.txt":                   f("site rsc"),
		"settings/security.html":     f("security page"),
		"_next/static/chunks/app.js": f("js"),
		".gitkeep":                   f(""),
	}
}

func TestResolution(t *testing.T) {
	h, err := New(testFS())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Built() {
		t.Fatal("Built() = false")
	}
	tests := []struct {
		path   string
		status int
		body   string
		ctype  string
		cache  string
	}{
		{"/", 200, "home", "text/html; charset=utf-8", "no-cache"},
		{"/site?id=abcdefghijkl", 200, "site page", "text/html; charset=utf-8", "no-cache"},
		{"/site.txt?_rsc=1", 200, "site rsc", "text/plain; charset=utf-8", "no-cache"},
		{"/settings/security", 200, "security page", "", ""},
		{"/_next/static/chunks/app.js", 200, "js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/settings", 404, "not found page", "text/html; charset=utf-8", "no-store"},
		{"/nope", 404, "not found page", "", ""},
		{"/site/extra", 404, "not found page", "", ""},
		{"/404", 404, "not found page", "", ""},
		{"/.gitkeep", 404, "not found page", "", ""},
		{"/site/", 308, "", "", ""},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
		if rec.Code != tt.status {
			t.Errorf("%s: status %d, want %d", tt.path, rec.Code, tt.status)
			continue
		}
		if tt.body != "" && rec.Body.String() != tt.body {
			t.Errorf("%s: body %q, want %q", tt.path, rec.Body, tt.body)
		}
		if tt.ctype != "" && rec.Header().Get("Content-Type") != tt.ctype {
			t.Errorf("%s: Content-Type %q, want %q", tt.path, rec.Header().Get("Content-Type"), tt.ctype)
		}
		if tt.cache != "" && rec.Header().Get("Cache-Control") != tt.cache {
			t.Errorf("%s: Cache-Control %q, want %q", tt.path, rec.Header().Get("Cache-Control"), tt.cache)
		}
	}
}

func TestTrailingSlashRedirectKeepsQuery(t *testing.T) {
	h, _ := New(testFS())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/site/?id=abcdefghijkl", nil))
	if loc := rec.Header().Get("Location"); loc != "/site?id=abcdefghijkl" {
		t.Errorf("Location = %q", loc)
	}
}

func TestETag(t *testing.T) {
	h, _ := New(testFS())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/site", nil))
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req := httptest.NewRequest("GET", "/site", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET: %d", rec.Code)
	}
}

func TestPlaceholderWhenNotBuilt(t *testing.T) {
	h, _ := New(fstest.MapFS{".gitkeep": &fstest.MapFile{}})
	if h.Built() {
		t.Fatal("Built() = true")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Errorf("/: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/site", nil))
	if rec.Code != 404 {
		t.Errorf("/site: %d", rec.Code)
	}
}

// Every exported page gets a CSP that allows exactly its own inline
// scripts (by hash), plus framing and base-URI protection.
func TestPageCSP(t *testing.T) {
	const theme, payload = `(()=>{document.documentElement.dataset.t="x"})()`, `self.__next_f.push([1,"a\u003cb"])`
	fsys := testFS()
	fsys["index.html"] = &fstest.MapFile{Data: []byte(`<!doctype html><html><head><script src="/_next/static/chunks/app.js" async=""></script>` +
		`<script>` + theme + `</script></head><body><p style="color:red">hi</p><script>` + payload + `</script><script>` + theme + `</script></body></html>`)}
	h, err := New(fsys)
	if err != nil {
		t.Fatal(err)
	}
	hash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	for _, p := range []string{"/", "/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "style-src 'self' 'unsafe-inline'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", p, csp, want)
			}
		}
		if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "'unsafe-eval'") {
			t.Errorf("%s: CSP allows arbitrary script: %q", p, csp)
		}
		if p == "/" {
			want := "script-src 'self' " + strings.Join(sortedStrings(hash(payload), hash(theme)), " ") + ";"
			if !strings.Contains(csp, want) {
				t.Errorf("CSP %q, want %q", csp, want)
			}
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: no X-Frame-Options", p)
		}
	}
	// Non-HTML files carry no page policy.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/_next/static/chunks/app.js", nil))
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("CSP on a script file")
	}
}

func sortedStrings(s ...string) []string { slices.Sort(s); return s }

// An https APP_ORIGIN pins the app host to HTTPS; plain http (local
// development) does not.
func TestHSTS(t *testing.T) {
	for _, origin := range []string{"https://dash.example.com", "http://dash.localhost:8080"} {
		u, _ := url.Parse(origin)
		d := &server.Deps{
			Config: &config.Config{AppOrigin: u, AppHost: u.Hostname(), SitesDomain: "sites.example.com"},
			Logger: slog.New(slog.DiscardHandler),
		}
		h, err := server.New(d, Register)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", origin+"/", nil))
		got := rec.Header().Get("Strict-Transport-Security")
		if want := u.Scheme == "https"; (got == "max-age=31536000") != want || (!want && got != "") {
			t.Errorf("%s: HSTS %q", origin, got)
		}
	}
}
