package sites

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanPath(t *testing.T) {
	ok := map[string]string{
		"index.html":         "index.html",
		"./index.html":       "index.html",
		"././a/b.css":        "a/b.css",
		"Docs/README.md":     "Docs/README.md",
		"a b/ü.html":         "a b/ü.html",
		".well-known/x.json": ".well-known/x.json",
		"_authors/x.html":    "_authors/x.html",
		"a/_auth/x":          "a/_auth/x",
		"...":                "...",
	}
	for in, want := range ok {
		got, err := CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "./", "/index.html", "//x", "a//b", "a/", "a/./b", ".", "..", "../x", "a/../b", "a/..",
		`a\b`, "a\x00b", "a\nb", "a\tb", "a\x7fb", "\xff.html", "_auth", "_auth/callback",
		strings.Repeat("a", MaxPathLen+1),
	}
	for _, in := range bad {
		_, err := CleanPath(in)
		var ie *InputError
		if !errors.As(err, &ie) {
			t.Errorf("CleanPath(%q) = %v, want *InputError", in, err)
		}
	}
	// Paths are case-sensitive: no folding.
	if got, _ := CleanPath("Index.HTML"); got != "Index.HTML" {
		t.Errorf("case folded: %q", got)
	}
}

func TestContentTypeFor(t *testing.T) {
	for p, want := range map[string]string{
		"index.html":       "text/html; charset=utf-8",
		"A.HTM":            "text/html; charset=utf-8",
		"app.css":          "text/css; charset=utf-8",
		"app.js":           "text/javascript; charset=utf-8",
		"mod.mjs":          "text/javascript; charset=utf-8",
		"data.json":        "application/json; charset=utf-8",
		"logo.svg":         "image/svg+xml; charset=utf-8",
		"photo.JPG":        "image/jpeg",
		"font.woff2":       "font/woff2",
		"app.wasm":         "application/wasm",
		"LICENSE":          "application/octet-stream",
		"x.unknownext123":  "application/octet-stream",
		"site.webmanifest": "application/manifest+json; charset=utf-8",
	} {
		if got := ContentTypeFor(p); got != want {
			t.Errorf("ContentTypeFor(%q) = %q, want %q", p, got, want)
		}
	}
	if got, err := contentType("x", "text/plain"); err != nil || got != "text/plain; charset=utf-8" {
		t.Errorf("given text/plain: %q %v", got, err)
	}
	if got, err := contentType("x", "text/plain; charset=latin1"); err != nil || got != "text/plain; charset=latin1" {
		t.Errorf("given charset: %q %v", got, err)
	}
	if _, err := contentType("x", "not a type"); err == nil {
		t.Error("invalid content type accepted")
	}
}
