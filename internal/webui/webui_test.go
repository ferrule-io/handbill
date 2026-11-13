package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
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
