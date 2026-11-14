package sites_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/storage"
)

type env struct {
	d     *server.Deps
	svc   *sites.Service
	owner uuid.UUID
	puts  *atomic.Int64
}

// countingStore counts Put calls to observe blob dedupe.
type countingStore struct {
	storage.Store
	puts *atomic.Int64
}

func (c countingStore) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	c.puts.Add(1)
	return c.Store.Put(ctx, key, r, size, ct)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := sitestest.Deps(t)
	puts := new(atomic.Int64)
	d.Storage = countingStore{Store: d.Storage, puts: puts}
	return &env{d: d, svc: sites.NewService(d), owner: sitestest.User(t, d, "owner@example.com"), puts: puts}
}

func (e *env) site(t *testing.T) sites.Site {
	t.Helper()
	s, err := e.svc.Create(t.Context(), e.owner, "Trip options", "Three itineraries")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func file(p, body string) sites.File { return sites.File{Path: p, Body: []byte(body)} }

// contents returns path → body of the site's current version.
func (e *env) contents(t *testing.T, siteID string) map[string]string {
	t.Helper()
	fis, err := e.svc.ListFiles(t.Context(), siteID)
	must(t, err)
	out := map[string]string{}
	for _, fi := range fis {
		obj, gotFI, err := e.svc.OpenFile(t.Context(), siteID, fi.Path)
		must(t, err)
		b, err := io.ReadAll(obj.Body)
		obj.Body.Close()
		must(t, err)
		if gotFI != fi {
			t.Errorf("OpenFile(%s) info %+v, ListFiles %+v", fi.Path, gotFI, fi)
		}
		out[fi.Path] = string(b)
	}
	return out
}

func wantContents(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("files %v, want %v", keys(got), keys(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q (files %v)", k, got[k], v, keys(got))
		}
	}
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func wantInputError(t *testing.T, what string, err error) {
	t.Helper()
	var ie *sites.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("%s: want *InputError, got %v", what, err)
	}
}

func TestCreateGetUpdateList(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)
	if len(s.ID) != 12 || s.OwnerID != e.owner || s.CurrentVersionID.Valid {
		t.Fatalf("created %+v", s)
	}
	got, err := e.svc.Get(ctx, s.ID)
	must(t, err)
	if got.Title != "Trip options" || got.Description != "Three itineraries" {
		t.Fatalf("get %+v", got)
	}
	role, err := e.svc.Role(ctx, s.ID, e.owner)
	must(t, err)
	if role != rbac.Owner {
		t.Fatalf("owner role %q", role)
	}
	other := sitestest.User(t, e.d, "other@example.com")
	if role, _ := e.svc.Role(ctx, s.ID, other); role != "" {
		t.Fatalf("non-member role %q", role)
	}
	if _, err := e.svc.Get(ctx, "aaaaaaaaaaaa"); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}

	title := "  Final itinerary  "
	up, err := e.svc.Update(ctx, s.ID, &title, nil)
	must(t, err)
	if up.Title != "Final itinerary" || up.Description != "Three itineraries" {
		t.Fatalf("update %+v", up)
	}
	empty := " "
	_, err = e.svc.Update(ctx, s.ID, &empty, nil)
	wantInputError(t, "empty title", err)
	_, err = e.svc.Create(ctx, e.owner, "", "")
	wantInputError(t, "create without title", err)

	s2 := e.site(t)
	sitestest.Member(t, e.d, s.ID, other, "viewer")
	ms, err := e.svc.ListForUser(ctx, e.owner)
	must(t, err)
	if len(ms) != 2 || ms[0].Site.ID != s2.ID || ms[1].Site.ID != s.ID || ms[0].Role != rbac.Owner {
		t.Fatalf("owner list %+v", ms)
	}
	ms, err = e.svc.ListForUser(ctx, other)
	must(t, err)
	if len(ms) != 1 || ms[0].Site.ID != s.ID || ms[0].Role != rbac.Viewer {
		t.Fatalf("viewer list %+v", ms)
	}

	a, err := e.svc.Access(ctx, s.ID, other)
	must(t, err)
	if a.SiteID != s.ID || a.Role != rbac.Viewer || a.PublicLinkEnabled {
		t.Fatalf("access %+v", a)
	}
	a, err = e.svc.Access(ctx, s.ID, uuid.Nil)
	must(t, err)
	if a.Role != "" {
		t.Fatalf("anonymous access %+v", a)
	}
	if _, err := e.svc.Access(ctx, "bbbbbbbbbbbb", other); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("access missing: %v", err)
	}
}

func TestPublishReplaceMergeDelete(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)

	v1, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{
		file("index.html", "<h1>v1</h1>"), file("./a.css", "body{}"), file("img/logo.svg", "<svg/>"),
	}, nil)
	must(t, err)
	if v1.FileCount != 3 || v1.Size != int64(len("<h1>v1</h1>body{}<svg/>")) || v1.PublishedBy != e.owner || v1.PublisherEmail != "owner@example.com" {
		t.Fatalf("v1 %+v", v1)
	}
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "<h1>v1</h1>", "a.css": "body{}", "img/logo.svg": "<svg/>"})

	v2, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Merge, []sites.File{
		file("index.html", "<h1>v2</h1>"), file("b.js", "x()"),
	}, []string{"a.css", "not-there.txt"})
	must(t, err)
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "<h1>v2</h1>", "b.js": "x()", "img/logo.svg": "<svg/>"})

	_, err = e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("only.html", "solo")}, nil)
	must(t, err)
	wantContents(t, e.contents(t, s.ID), map[string]string{"only.html": "solo"})

	vs, err := e.svc.ListVersions(ctx, s.ID)
	must(t, err)
	if len(vs) != 3 || vs[1].ID != v2.ID || vs[2].ID != v1.ID || vs[0].FileCount != 1 {
		t.Fatalf("versions %+v", vs)
	}
	fis, err := e.svc.ListFiles(ctx, s.ID)
	must(t, err)
	if fis[0].ContentType != "text/html; charset=utf-8" {
		t.Fatalf("content type %q", fis[0].ContentType)
	}

	// Replace deployments take no deletions, and must contain files.
	_, err = e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("x.html", "x")}, []string{"only.html"})
	wantInputError(t, "replace with deletions", err)
	_, err = e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, nil, nil)
	wantInputError(t, "empty replace", err)
	// A merge that deletes everything would publish an empty version.
	_, err = e.svc.Publish(ctx, s.ID, e.owner, sites.Merge, nil, []string{"only.html"})
	wantInputError(t, "merge deleting all", err)
	_, err = e.svc.Publish(ctx, s.ID, e.owner, "upsert", []sites.File{file("x.html", "x")}, nil)
	wantInputError(t, "bad mode", err)
	_, err = e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("../x.html", "x")}, nil)
	wantInputError(t, "bad path", err)
	_, err = e.svc.Publish(ctx, "cccccccccccc", e.owner, sites.Replace, []sites.File{file("x.html", "x")}, nil)
	if !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("publish to missing site: %v", err)
	}
	// Failed publishes changed nothing.
	wantContents(t, e.contents(t, s.ID), map[string]string{"only.html": "solo"})
}

func TestStagedDeployment(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)
	dep, err := e.svc.CreateDeployment(ctx, s.ID, e.owner, sites.Replace)
	must(t, err)
	must(t, e.svc.AddFiles(ctx, dep, e.owner, []sites.File{file("index.html", "a"), file("index.html", "b")}))
	must(t, e.svc.AddFiles(ctx, dep, e.owner, []sites.File{file("app.js", "c"), {Path: "data", ContentType: "application/json", Body: []byte("{}")}}))
	wantInputError(t, "replace delete", e.svc.DeletePaths(ctx, dep, e.owner, []string{"x"}))
	// Only the creator adds to or finalizes it, not even another manager.
	other := sitestest.User(t, e.d, "manager@example.com")
	sitestest.Member(t, e.d, s.ID, other, "manager")
	for what, err := range map[string]error{
		"add":     e.svc.AddFiles(ctx, dep, other, []sites.File{file("index.html", "theirs")}),
		"delete":  e.svc.DeletePaths(ctx, dep, other, []string{"x"}),
		"archive": e.svc.AddArchive(ctx, dep, other, bytes.NewReader(tarGz(t, entry{name: "index.html", body: "theirs"})), "tar.gz"),
		"nobody":  e.svc.AddFiles(ctx, dep, uuid.Nil, []sites.File{file("index.html", "theirs")}),
	} {
		if !errors.Is(err, sites.ErrNotCreator) {
			t.Fatalf("%s by another account: %v", what, err)
		}
	}
	if _, err := e.svc.Finalize(ctx, dep, other); !errors.Is(err, sites.ErrNotCreator) {
		t.Fatalf("finalize by another account: %v", err)
	}
	// Nothing is visible before finalize.
	if fis, _ := e.svc.ListFiles(ctx, s.ID); len(fis) != 0 {
		t.Fatalf("files before finalize: %v", fis)
	}
	v, err := e.svc.Finalize(ctx, dep, e.owner)
	must(t, err)
	if v.FileCount != 3 || v.PublishedBy != e.owner {
		t.Fatalf("version %+v", v)
	}
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "b", "app.js": "c", "data": "{}"})
	fis, _ := e.svc.ListFiles(ctx, s.ID)
	if i := slices.IndexFunc(fis, func(f sites.FileInfo) bool { return f.Path == "data" }); fis[i].ContentType != "application/json; charset=utf-8" {
		t.Fatalf("given content type became %q", fis[i].ContentType)
	}

	if _, err := e.svc.Finalize(ctx, dep, e.owner); !errors.Is(err, sites.ErrDeploymentClosed) {
		t.Fatalf("second finalize: %v", err)
	}
	if err := e.svc.AddFiles(ctx, dep, e.owner, []sites.File{file("late.html", "x")}); !errors.Is(err, sites.ErrDeploymentClosed) {
		t.Fatalf("add after finalize: %v", err)
	}
	if _, err := e.svc.Finalize(ctx, uuid.New(), e.owner); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("finalize unknown: %v", err)
	}

	// Merge: a deletion followed by a re-add keeps the file.
	dep, err = e.svc.CreateDeployment(ctx, s.ID, e.owner, sites.Merge)
	must(t, err)
	must(t, e.svc.DeletePaths(ctx, dep, e.owner, []string{"app.js", "./data"}))
	must(t, e.svc.AddFiles(ctx, dep, e.owner, []sites.File{file("app.js", "d")}))
	_, err = e.svc.Finalize(ctx, dep, e.owner)
	must(t, err)
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "b", "app.js": "d"})
}

func TestMergeUsesCurrentVersionAtFinalize(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)
	_, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("index.html", "base")}, nil)
	must(t, err)
	// Opened before another publish lands, finalized after it.
	dep, err := e.svc.CreateDeployment(ctx, s.ID, e.owner, sites.Merge)
	must(t, err)
	must(t, e.svc.AddFiles(ctx, dep, e.owner, []sites.File{file("late.html", "late")}))
	_, err = e.svc.Publish(ctx, s.ID, e.owner, sites.Merge, []sites.File{file("early.html", "early")}, nil)
	must(t, err)
	_, err = e.svc.Finalize(ctx, dep, e.owner)
	must(t, err)
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "base", "early.html": "early", "late.html": "late"})
}

func TestConcurrentMergesKeepEveryFile(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)
	_, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("index.html", "base")}, nil)
	must(t, err)
	const n = 12
	want := map[string]string{"index.html": "base"}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		p := fmt.Sprintf("page-%02d.html", i)
		want[p] = p
		wg.Go(func() {
			dep, err := e.svc.CreateDeployment(ctx, s.ID, e.owner, sites.Merge)
			if err == nil {
				err = e.svc.AddFiles(ctx, dep, e.owner, []sites.File{file(p, p)})
			}
			if err == nil {
				_, err = e.svc.Finalize(ctx, dep, e.owner)
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	wantContents(t, e.contents(t, s.ID), want)
	vs, _ := e.svc.ListVersions(ctx, s.ID)
	if len(vs) != n+1 || vs[0].FileCount != n+1 {
		t.Fatalf("%d versions, newest has %d files", len(vs), vs[0].FileCount)
	}
}

func TestBlobDedupe(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)
	same := "identical bytes"
	_, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("a.txt", same), file("b/c.txt", same)}, nil)
	must(t, err)
	if n := e.puts.Load(); n != 1 {
		t.Fatalf("%d puts for one distinct blob", n)
	}
	_, err = e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("d.txt", same), file("e.txt", "new")}, nil)
	must(t, err)
	if n := e.puts.Load(); n != 2 {
		t.Fatalf("%d puts after republishing an existing blob", n)
	}
	var rows int
	must(t, e.d.DB.QueryRow(ctx, "SELECT count(*) FROM site_blobs WHERE site_id = $1", s.ID).Scan(&rows))
	if rows != 2 {
		t.Fatalf("%d site_blobs rows", rows)
	}
	// Blobs are per site: another site uploads its own copy.
	s2 := e.site(t)
	_, err = e.svc.Publish(ctx, s2.ID, e.owner, sites.Replace, []sites.File{file("a.txt", same)}, nil)
	must(t, err)
	if n := e.puts.Load(); n != 3 {
		t.Fatalf("%d puts after publishing to a second site", n)
	}
	sum := sha256.Sum256([]byte(same))
	if _, err := e.d.Storage.Stat(ctx, "sites/"+s.ID+"/blobs/"+hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("blob not at its content address: %v", err)
	}
}

func TestRollback(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)
	v1, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("index.html", "one"), file("old.html", "o")}, nil)
	must(t, err)
	v2, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("index.html", "two")}, nil)
	must(t, err)
	must(t, e.svc.Rollback(ctx, s.ID, v1.ID))
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "one", "old.html": "o"})
	got, _ := e.svc.Get(ctx, s.ID)
	if got.CurrentVersionID.UUID != v1.ID {
		t.Fatal("current version not repointed")
	}
	vs, _ := e.svc.ListVersions(ctx, s.ID)
	if len(vs) != 2 {
		t.Fatalf("rollback created a version: %d", len(vs))
	}
	must(t, e.svc.Rollback(ctx, s.ID, v2.ID))
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "two"})

	other := e.site(t)
	ov, err := e.svc.Publish(ctx, other.ID, e.owner, sites.Replace, []sites.File{file("index.html", "other")}, nil)
	must(t, err)
	if err := e.svc.Rollback(ctx, s.ID, ov.ID); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("rollback to another site's version: %v", err)
	}
	if err := e.svc.Rollback(ctx, s.ID, uuid.New()); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("rollback to unknown version: %v", err)
	}
}

func TestDeleteRemovesStoragePrefix(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.site(t)
	keep := e.site(t)
	_, err := e.svc.Publish(ctx, s.ID, e.owner, sites.Replace, []sites.File{file("index.html", "gone")}, nil)
	must(t, err)
	_, err = e.svc.Publish(ctx, keep.ID, e.owner, sites.Replace, []sites.File{file("index.html", "kept")}, nil)
	must(t, err)
	sum := sha256.Sum256([]byte("gone"))
	key := "sites/" + s.ID + "/blobs/" + hex.EncodeToString(sum[:])
	if _, err := e.d.Storage.Stat(ctx, key); err != nil {
		t.Fatal(err)
	}
	must(t, e.svc.Delete(ctx, s.ID))
	if _, err := e.d.Storage.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("blob after delete: %v", err)
	}
	if _, err := e.svc.Get(ctx, s.ID); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := e.svc.Delete(ctx, s.ID); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	wantContents(t, e.contents(t, keep.ID), map[string]string{"index.html": "kept"})
}

// archive entries for the builders below.
type entry struct {
	name, body string
	kind       byte // tar type flag
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, en := range entries {
		h := &tar.Header{Name: en.name, Typeflag: en.kind, Mode: 0o644, Size: int64(len(en.body))}
		switch en.kind {
		case 0:
			h.Typeflag = tar.TypeReg
		case tar.TypeSymlink, tar.TypeLink:
			h.Linkname, h.Size = en.body, 0
		case tar.TypeDir, tar.TypeChar, tar.TypeFifo:
			h.Size = 0
		}
		must(t, tw.WriteHeader(h))
		if h.Size > 0 {
			_, err := io.WriteString(tw, en.body)
			must(t, err)
		}
	}
	must(t, tw.Close())
	must(t, zw.Close())
	return buf.Bytes()
}

func zipArchive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, en := range entries {
		h := &zip.FileHeader{Name: en.name, Method: zip.Deflate}
		switch en.kind {
		case tar.TypeSymlink:
			h.SetMode(0o777 | fs.ModeSymlink)
		case tar.TypeDir:
			h.SetMode(0o755 | fs.ModeDir)
		}
		w, err := zw.CreateHeader(h)
		must(t, err)
		_, err = io.WriteString(w, en.body)
		must(t, err)
	}
	must(t, zw.Close())
	return buf.Bytes()
}

func (e *env) publishArchive(t *testing.T, siteID string, data []byte, format string) error {
	t.Helper()
	dep, err := e.svc.CreateDeployment(t.Context(), siteID, e.owner, sites.Replace)
	must(t, err)
	if err := e.svc.AddArchive(t.Context(), dep, e.owner, bytes.NewReader(data), format); err != nil {
		return err
	}
	_, err = e.svc.Finalize(t.Context(), dep, e.owner)
	return err
}

func TestArchive(t *testing.T) {
	e := newEnv(t)
	s := e.site(t)
	site := []entry{
		{name: "site/", kind: tar.TypeDir},
		{name: "site/index.html", body: "<h1>home</h1>"},
		{name: "site/css/", kind: tar.TypeDir},
		{name: "site/css/app.css", body: "h1{}"},
	}
	stripped := map[string]string{"index.html": "<h1>home</h1>", "css/app.css": "h1{}"}

	must(t, e.publishArchive(t, s.ID, tarGz(t, site...), "tar.gz"))
	wantContents(t, e.contents(t, s.ID), stripped)
	must(t, e.publishArchive(t, s.ID, zipArchive(t, site...), "zip"))
	wantContents(t, e.contents(t, s.ID), stripped)

	// "./"-relative archives and archives with root-level files keep paths.
	must(t, e.publishArchive(t, s.ID, tarGz(t, entry{name: "./", kind: tar.TypeDir}, entry{name: "./index.html", body: "dot"}, entry{name: "./a/b.txt", body: "b"}), "tar.gz"))
	wantContents(t, e.contents(t, s.ID), map[string]string{"index.html": "dot", "a/b.txt": "b"})
	must(t, e.publishArchive(t, s.ID, zipArchive(t, entry{name: "README", body: "r"}, entry{name: "site/index.html", body: "i"}), "zip"))
	wantContents(t, e.contents(t, s.ID), map[string]string{"README": "r", "site/index.html": "i"})
}

func TestArchiveRejects(t *testing.T) {
	e := newEnv(t)
	s := e.site(t)
	big := string(make([]byte, sites.MaxFileSize+1))
	cases := []struct {
		name    string
		entries []entry
		zipOK   bool // the case does not apply to zip
	}{
		{"traversal", []entry{{name: "index.html", body: "x"}, {name: "../evil.html", body: "x"}}, false},
		{"nested traversal", []entry{{name: "a/../../evil.html", body: "x"}}, false},
		{"absolute", []entry{{name: "/etc/passwd", body: "x"}}, false},
		{"backslash", []entry{{name: `a\..\evil.html`, body: "x"}}, false},
		{"symlink", []entry{{name: "index.html", body: "x"}, {name: "link", body: "/etc/passwd", kind: tar.TypeSymlink}}, false},
		{"hardlink", []entry{{name: "link", body: "index.html", kind: tar.TypeLink}}, true},
		{"device", []entry{{name: "dev", kind: tar.TypeChar}}, true},
		{"fifo", []entry{{name: "pipe", kind: tar.TypeFifo}}, true},
		{"reserved", []entry{{name: "_auth/callback", body: "x"}}, false},
		{"too large", []entry{{name: "big.bin", body: big}}, false},
		{"empty", []entry{{name: "dir/", kind: tar.TypeDir}}, false},
	}
	for _, tc := range cases {
		wantInputError(t, tc.name+" tar.gz", e.publishArchive(t, s.ID, tarGz(t, tc.entries...), "tar.gz"))
		if !tc.zipOK {
			wantInputError(t, tc.name+" zip", e.publishArchive(t, s.ID, zipArchive(t, tc.entries...), "zip"))
		}
	}
	var many []entry
	for i := range sites.MaxArchiveEntries + 1 {
		many = append(many, entry{name: fmt.Sprintf("d%d/", i), kind: tar.TypeDir})
	}
	wantInputError(t, "too many entries", e.publishArchive(t, s.ID, tarGz(t, many...), "tar.gz"))
	wantInputError(t, "garbage", e.publishArchive(t, s.ID, []byte("not an archive"), "tar.gz"))
	wantInputError(t, "garbage zip", e.publishArchive(t, s.ID, []byte("not an archive"), "zip"))
	wantInputError(t, "format", e.publishArchive(t, s.ID, nil, "rar"))
	if fis, _ := e.svc.ListFiles(t.Context(), s.ID); len(fis) != 0 {
		t.Fatalf("rejected archives published files: %v", fis)
	}
}

func wantInputErrorContaining(t *testing.T, what string, err error, want string) {
	t.Helper()
	var ie *sites.InputError
	if !errors.As(err, &ie) || !strings.Contains(ie.Msg, want) {
		t.Fatalf("%s: want *InputError containing %q, got %v", what, want, err)
	}
}

// rawZip returns a zip whose central directory holds records headers (empty
// names, all pointing at one stored file at offset 0) while its end record
// declares declared entries; zip64 declares them in a zip64 end record.
func rawZip(records int, declared uint64, zip64 bool) []byte {
	le := binary.LittleEndian
	var b bytes.Buffer
	local := make([]byte, 30)
	le.PutUint32(local, 0x04034b50)
	le.PutUint16(local[4:], 20)
	le.PutUint16(local[26:], 5)
	b.Write(local)
	b.WriteString("a.txt")
	cdOffset := b.Len()
	central := make([]byte, 46)
	le.PutUint32(central, 0x02014b50)
	le.PutUint16(central[4:], 20)
	le.PutUint16(central[6:], 20)
	for range records {
		b.Write(central)
	}
	cdSize := b.Len() - cdOffset
	count := uint16(declared)
	if zip64 {
		count = 0xffff
		end64 := make([]byte, 56)
		le.PutUint32(end64, 0x06064b50)
		le.PutUint64(end64[4:], 44)
		le.PutUint16(end64[12:], 45)
		le.PutUint16(end64[14:], 45)
		le.PutUint64(end64[24:], declared)
		le.PutUint64(end64[32:], declared)
		le.PutUint64(end64[40:], uint64(cdSize))
		le.PutUint64(end64[48:], uint64(cdOffset))
		locator := make([]byte, 20)
		le.PutUint32(locator, 0x07064b50)
		le.PutUint64(locator[8:], uint64(b.Len()))
		le.PutUint32(locator[16:], 1)
		b.Write(end64)
		b.Write(locator)
	}
	end := make([]byte, 22)
	le.PutUint32(end, 0x06054b50)
	le.PutUint16(end[8:], count)
	le.PutUint16(end[10:], count)
	le.PutUint32(end[12:], uint32(cdSize))
	le.PutUint32(end[16:], uint32(cdOffset))
	b.Write(end)
	return b.Bytes()
}

func TestZipCentralDirectoryBomb(t *testing.T) {
	e := newEnv(t)
	s := e.site(t)
	dep, err := e.svc.CreateDeployment(t.Context(), s.ID, e.owner, sites.Replace)
	must(t, err)
	addZip := func(data []byte) error {
		return e.svc.AddArchive(t.Context(), dep, e.owner, bytes.NewReader(data), "zip")
	}

	// archive/zip compares only the low 16 bits of the declared count and
	// reads every header in the directory: 200,000 records declared as
	// 200,000 mod 65536. Rejected before any per-entry allocation.
	bomb := rawZip(200_000, 200_000, false)
	var bombErr error
	allocs := testing.AllocsPerRun(1, func() { bombErr = addZip(bomb) })
	if allocs > sites.MaxArchiveEntries {
		t.Fatalf("wrapped count: %v allocations, want fewer than %d", allocs, sites.MaxArchiveEntries)
	}
	wantInputErrorContaining(t, "wrapped count", bombErr, "more than")

	// A declared count over the cap, here through a zip64 end record.
	wantInputErrorContaining(t, "zip64 declared", addZip(rawZip(1, sites.MaxArchiveEntries+1, true)), "more than")
}

// paxRecord returns one PAX extended-header record, "<len> key=value\n".
func paxRecord(key, value string) string {
	rest := " " + key + "=" + value + "\n"
	n := len(rest) + 1
	for len(strconv.Itoa(n))+len(rest) != n {
		n++
	}
	return strconv.Itoa(n) + rest
}

// ustarHeader returns a raw 512-byte ustar header block.
func ustarHeader(name string, flag byte, size int) []byte {
	h := make([]byte, 512)
	copy(h, name)
	copy(h[100:], "0000644\x00")
	copy(h[124:], fmt.Sprintf("%011o\x00", size))
	copy(h[136:], fmt.Sprintf("%011o\x00", 0))
	h[156] = flag
	copy(h[257:], "ustar\x0000")
	copy(h[148:], "        ")
	sum := 0
	for _, c := range h {
		sum += int(c)
	}
	copy(h[148:], fmt.Sprintf("%06o\x00 ", sum))
	return h
}

func TestTarGzMetadataBomb(t *testing.T) {
	e := newEnv(t)
	s := e.site(t)
	sites.SetMaxTarStream(t, 1<<20)
	// 32 chained PAX headers of 64 KiB each, none an entry, then one small
	// file: 2 MiB decompressed against a 1 MiB stream limit.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	rec := paxRecord("comment", strings.Repeat("x", 64<<10))
	for range 32 {
		_, err := zw.Write(ustarHeader("PaxHeaders/x", tar.TypeXHeader, len(rec)))
		must(t, err)
		_, err = zw.Write(append([]byte(rec), make([]byte, -len(rec)&511)...))
		must(t, err)
	}
	tw := tar.NewWriter(zw)
	must(t, tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte("hi"))
	must(t, err)
	must(t, tw.Close())
	must(t, zw.Close())
	wantInputErrorContaining(t, "PAX chain", e.publishArchive(t, s.ID, buf.Bytes(), "tar.gz"), "archive is larger than")

	// A plain archive under the lowered limit still publishes.
	must(t, e.publishArchive(t, s.ID, tarGz(t, entry{name: "index.html", body: "ok"}), "tar.gz"))
}

// hookStore runs beforePut ahead of every Put.
type hookStore struct {
	storage.Store
	beforePut func()
}

func (h hookStore) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	h.beforePut()
	return h.Store.Put(ctx, key, r, size, ct)
}

// A site deleted while a blob upload is in flight, after sites.Delete
// cleared its storage prefix, leaves no object behind.
func TestUploadRacingSiteDelete(t *testing.T) {
	d := sitestest.Deps(t)
	var svc *sites.Service
	var siteID string
	d.Storage = hookStore{Store: d.Storage, beforePut: func() { must(t, svc.Delete(t.Context(), siteID)) }}
	svc = sites.NewService(d)
	owner := sitestest.User(t, d, "owner@example.com")
	s, err := svc.Create(t.Context(), owner, "Doomed", "")
	must(t, err)
	siteID = s.ID
	dep, err := svc.CreateDeployment(t.Context(), s.ID, owner, sites.Replace)
	must(t, err)
	if err := svc.AddFiles(t.Context(), dep, owner, []sites.File{file("index.html", "late")}); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("AddFiles after site delete: %v, want ErrNotFound", err)
	}
	sum := sha256.Sum256([]byte("late"))
	if _, err := d.Storage.Stat(t.Context(), "sites/"+s.ID+"/blobs/"+hex.EncodeToString(sum[:])); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("blob left behind: %v", err)
	}
}
