package upload_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/token"
	"github.com/ferrule-io/handbill/internal/upload"
)

type fixture struct {
	t       *testing.T
	d       *server.Deps
	h       http.Handler
	svc     *sites.Service
	signer  *token.Signer
	manager uuid.UUID
	site    string
}

func newFixture(t *testing.T, opts ...func(*server.Deps)) *fixture {
	d := sitestest.Deps(t)
	for _, o := range opts {
		o(d)
	}
	h, err := server.New(d, upload.Register)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, d: d, h: h, svc: sites.NewService(d), signer: token.NewSigner(d.Config.SecretKey)}
	owner := sitestest.User(t, d, "owner@example.com")
	f.manager = sitestest.User(t, d, "manager@example.com")
	s, err := f.svc.Create(t.Context(), owner, "Site", "")
	if err != nil {
		t.Fatal(err)
	}
	f.site = s.ID
	sitestest.Member(t, d, s.ID, f.manager, "manager")
	return f
}

func (f *fixture) deployment(site string) uuid.UUID {
	f.t.Helper()
	id, err := f.svc.CreateDeployment(f.t.Context(), site, f.manager, sites.Replace)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) url(dep uuid.UUID, site string, user uuid.UUID, expires time.Time) string {
	return upload.URL(f.d.Config, f.signer, token.UploadGrant{DeploymentID: dep, SiteID: site, UserID: user, Expires: expires})
}

func (f *fixture) fileURL(dep uuid.UUID, site string, user uuid.UUID, path string, expires time.Time) string {
	return upload.URL(f.d.Config, f.signer, token.UploadGrant{DeploymentID: dep, SiteID: site, UserID: user, Path: path, Expires: expires})
}

// put sends the body and returns the status and error code.
func (f *fixture) put(rawURL string, body []byte) (int, string) {
	f.t.Helper()
	return f.send(rawURL, bytes.NewReader(body), int64(len(body)))
}

// send sends body with a declared length (-1: unknown, as when chunked).
func (f *fixture) send(rawURL string, body io.Reader, length int64) (int, string) {
	f.t.Helper()
	u, _ := url.Parse(rawURL)
	req := httptest.NewRequest("PUT", u.RequestURI(), body)
	req.ContentLength = length
	req.Host = u.Host
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	var out struct{ Error string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Error
}

func tarGz(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func zipArchive(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestUpload(t *testing.T) {
	f := newFixture(t)
	exp := time.Now().Add(upload.GrantTTL)

	dep := f.deployment(f.site)
	if s, e := f.put(f.url(dep, f.site, f.manager, exp), tarGz(map[string]string{"site/index.html": "hi", "site/css/a.css": "a"})); s != 200 {
		t.Fatalf("tar.gz: %d %s", s, e)
	}
	if s, e := f.put(f.url(dep, f.site, f.manager, exp), zipArchive(map[string]string{"extra.txt": "x"})); s != 200 {
		t.Fatalf("zip: %d %s", s, e)
	}
	v, err := f.svc.Finalize(t.Context(), dep, f.manager)
	if err != nil || v.FileCount != 3 {
		t.Fatalf("finalize: %+v %v", v, err)
	}
	if fi, err := f.svc.FindFile(t.Context(), v.ID, "index.html"); err != nil || fi.Size != 2 {
		t.Fatalf("stripped common dir: %+v %v", fi, err)
	}
	if s, e := f.put(f.url(dep, f.site, f.manager, exp), tarGz(map[string]string{"a": "a"})); s != 409 || e != "deployment_closed" {
		t.Fatalf("finalized: %d %s", s, e)
	}

	dep = f.deployment(f.site)
	body := tarGz(map[string]string{"index.html": "x"})
	if s, e := f.put(f.url(dep, f.site, f.manager, time.Now().Add(-time.Second)), body); s != 403 || e != "expired_grant" {
		t.Fatalf("expired: %d %s", s, e)
	}
	good := f.url(dep, f.site, f.manager, exp)
	if s, e := f.put(good[:len(good)-2]+"xx", body); s != 403 || e != "invalid_grant" {
		t.Fatalf("tampered: %d %s", s, e)
	}
	other := f.deployment(f.site)
	if s, e := f.put(strings.Replace(good, dep.String(), other.String(), 1), body); s != 403 || e != "invalid_grant" {
		t.Fatalf("path id mismatch: %d %s", s, e)
	}
	if s, e := f.put(good, []byte("just some text")); s != 415 || e != "unsupported_format" {
		t.Fatalf("plain text: %d %s", s, e)
	}
	if s, e := f.put(good, append([]byte{0x1f, 0x8b}, "not gzip"...)); s != 400 || e != "invalid_archive" {
		t.Fatalf("bad gzip: %d %s", s, e)
	}

	// A grant for another site's deployment.
	s2, _ := f.svc.Create(t.Context(), f.manager, "Other", "")
	dep2 := f.deployment(s2.ID)
	if s, e := f.put(f.url(dep2, f.site, f.manager, exp), body); s != 404 || e != "not_found" {
		t.Fatalf("other site: %d %s", s, e)
	}

	// A grant for another account, even one who can publish: a deployment
	// belongs to its creator.
	second := sitestest.User(t, f.d, "second@example.com")
	sitestest.Member(t, f.d, f.site, second, "manager")
	if s, e := f.put(f.url(dep, f.site, second, exp), body); s != 403 || e != "forbidden" {
		t.Fatalf("another account's grant: %d %s", s, e)
	}

	// The grant's user removed from the site within its lifetime.
	third := sitestest.User(t, f.d, "third@example.com")
	sitestest.Member(t, f.d, f.site, third, "manager")
	dep3, err := f.svc.CreateDeployment(t.Context(), f.site, third, sites.Replace)
	if err != nil {
		t.Fatal(err)
	}
	removed := f.url(dep3, f.site, third, exp)
	if _, err := f.d.DB.Exec(t.Context(), `DELETE FROM memberships WHERE site_id = $1 AND user_id = $2`, f.site, third); err != nil {
		t.Fatal(err)
	}
	if s, e := f.put(removed, body); s != 403 || e != "forbidden" {
		t.Fatalf("removed: %d %s", s, e)
	}

	// Over the limit.
	f.d.Config.Limits.MaxArchiveSize = 64
	big := tarGz(map[string]string{"index.html": strings.Repeat("random-ish content ", 500) + uuid.NewString()})
	if s, e := f.put(good, big); s != 413 || e != "too_large" {
		t.Fatalf("too large: %d %s", s, e)
	}

	// The creator demoted to viewer.
	if _, err := f.d.DB.Exec(t.Context(), `UPDATE memberships SET role = 'viewer' WHERE site_id = $1 AND user_id = $2`, f.site, f.manager); err != nil {
		t.Fatal(err)
	}
	if s, e := f.put(good, body); s != 403 || e != "forbidden" {
		t.Fatalf("demoted: %d %s", s, e)
	}
}

func TestFileUpload(t *testing.T) {
	f := newFixture(t)
	exp := time.Now().Add(upload.GrantTTL)

	dep := f.deployment(f.site)
	clip := f.fileURL(dep, f.site, f.manager, "media/clip.mp4", exp)
	if s, e := f.put(clip, []byte("\x00\x01binary")); s != 200 {
		t.Fatalf("mp4: %d %s", s, e)
	}
	if s, e := f.put(f.fileURL(dep, f.site, f.manager, "docs/a.pdf", exp), []byte("%PDF-1.7")); s != 200 {
		t.Fatalf("pdf: %d %s", s, e)
	}

	// A grant works only on its own endpoint.
	if s, e := f.put(strings.Replace(clip, "/file?", "/archive?", 1), tarGz(map[string]string{"a": "a"})); s != 403 || e != "invalid_grant" {
		t.Fatalf("file grant on /archive: %d %s", s, e)
	}
	archive := f.url(dep, f.site, f.manager, exp)
	if s, e := f.put(strings.Replace(archive, "/archive?", "/file?", 1), []byte("x")); s != 403 || e != "invalid_grant" {
		t.Fatalf("archive grant on /file: %d %s", s, e)
	}
	if s, e := f.put(f.fileURL(dep, f.site, f.manager, "_auth/x", exp), []byte("x")); s != 400 || e != "invalid_file" {
		t.Fatalf("reserved path: %d %s", s, e)
	}

	v, err := f.svc.Finalize(t.Context(), dep, f.manager)
	if err != nil || v.FileCount != 2 {
		t.Fatalf("finalize: %+v %v", v, err)
	}
	if fi, err := f.svc.FindFile(t.Context(), v.ID, "media/clip.mp4"); err != nil || fi.Size != 8 || fi.ContentType != "video/mp4" {
		t.Fatalf("clip: %+v %v", fi, err)
	}
	if s, e := f.put(clip, []byte("again")); s != 409 || e != "deployment_closed" {
		t.Fatalf("finalized: %d %s", s, e)
	}
}

func TestFileUploadLimit(t *testing.T) {
	f := newFixture(t, func(d *server.Deps) { d.Config.Limits.MaxFileSize = 8 })
	exp := time.Now().Add(upload.GrantTTL)
	u := f.fileURL(f.deployment(f.site), f.site, f.manager, "a.bin", exp)

	if s, e := f.put(u, []byte("123456789")); s != 413 || e != "too_large" {
		t.Fatalf("declared 9 bytes: %d %s", s, e)
	}
	// Without a declared length the body is cut at the limit, not truncated.
	if s, e := f.send(u, strings.NewReader("123456789"), -1); s != 413 || e != "too_large" {
		t.Fatalf("undeclared 9 bytes: %d %s", s, e)
	}
	if s, e := f.send(u, strings.NewReader("12345678"), -1); s != 200 {
		t.Fatalf("undeclared 8 bytes: %d %s", s, e)
	}
	if s, e := f.put(u, []byte("12345678")); s != 200 {
		t.Fatalf("8 bytes: %d %s", s, e)
	}
}
