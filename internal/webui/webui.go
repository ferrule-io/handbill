// Package webui serves the statically exported Next.js UI embedded from
// dist/ (populated by `make web` or the Dockerfile; see the Makefile).
//
// Resolution follows docs/design.md: "/" → index.html, "/<route>" →
// "<route>.html", any other existing file (RSC payloads, _next assets) as-is,
// and everything else → the export's 404.html with status 404. There is no
// catch-all HTML fallback.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/ferrule-io/handbill/internal/server"
)

// dist holds the export. `all:` keeps _next/ and other underscore paths.
//
//go:embed all:dist
var dist embed.FS

// Register serves the UI on the app host for every path no other module
// claims ("GET /" is the least specific ServeMux pattern).
func Register(r *server.Routes, d *server.Deps) error {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return err
	}
	h, err := New(sub)
	if err != nil {
		return err
	}
	if !h.Built() {
		d.Logger.Warn("web UI not built; serving a placeholder (run `make web`)")
	}
	r.App.Handle("GET /", h)
	return nil
}

type file struct {
	body        []byte
	etag        string
	contentType string
}

// Handler serves an exported UI tree.
type Handler struct {
	files    map[string]*file // keyed by slash path without leading slash
	notFound *file
}

// New indexes every regular file in fsys. The export is small and already
// in memory, so bodies and ETags are computed once here.
func New(fsys fs.FS) (*Handler, error) {
	h := &Handler{files: map[string]*file{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		h.files[p] = &file{
			body:        b,
			etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
			contentType: contentType(p),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.notFound = h.files["404.html"]
	return h, nil
}

// Built reports whether fsys contained an export (index.html and 404.html).
func (h *Handler) Built() bool {
	return h.files["index.html"] != nil && h.notFound != nil
}

func contentType(p string) string {
	switch path.Ext(p) {
	case ".txt":
		// RSC payloads; Next's client accepts text/plain from static exports.
		return "text/plain; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	}
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.Built() {
		h.servePlaceholder(w, r)
		return
	}
	name, f := h.resolve(r.URL.Path)
	if f == nil {
		// "/site/" → "/site" when that route exists; never a fallback page.
		if trimmed := strings.TrimSuffix(r.URL.Path, "/"); trimmed != r.URL.Path && trimmed != "" {
			if _, ok := h.lookup(trimmed); ok {
				target := trimmed
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
				return
			}
		}
		h.write(w, r, http.StatusNotFound, "404.html", h.notFound)
		return
	}
	status := http.StatusOK
	if name == "404.html" || strings.HasPrefix(name, "_not-found") {
		status = http.StatusNotFound
	}
	h.write(w, r, status, name, f)
}

// resolve maps a request path to an exported file.
func (h *Handler) resolve(urlPath string) (string, *file) {
	if strings.HasSuffix(urlPath, "/") && urlPath != "/" {
		return "", nil
	}
	name, ok := h.lookup(urlPath)
	if !ok {
		return "", nil
	}
	return name, h.files[name]
}

func (h *Handler) lookup(urlPath string) (string, bool) {
	p := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if p == "" {
		p = "index"
	}
	if _, ok := h.files[p]; ok {
		return p, true
	}
	if _, ok := h.files[p+".html"]; ok {
		return p + ".html", true
	}
	return "", false
}

func (h *Handler) write(w http.ResponseWriter, r *http.Request, status int, name string, f *file) {
	hdr := w.Header()
	hdr.Set("Content-Type", f.contentType)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if strings.HasPrefix(name, "_next/static/") {
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hdr.Set("Cache-Control", "no-cache")
	}
	if status != http.StatusOK {
		hdr.Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write(f.body)
		}
		return
	}
	hdr.Set("ETag", f.etag)
	// ServeContent handles If-None-Match, Range, and HEAD.
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.body))
}

const placeholder = `<!doctype html><html lang="en"><meta charset="utf-8">
<title>handbill</title>
<body style="font:16px/1.5 system-ui;max-width:36rem;margin:4rem auto;padding:0 1rem">
<h1>handbill</h1>
<p>The web UI is not built into this binary. Run <code>make web</code> and rebuild,
or use the container image.</p>
</body></html>
`

func (h *Handler) servePlaceholder(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(placeholder))
}
