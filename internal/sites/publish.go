package sites

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/db"
)

// Mode is how a deployment's files combine with the site's current version.
type Mode string

const (
	// Replace publishes exactly the deployment's files.
	Replace Mode = "replace"
	// Merge publishes the current version (as of finalize) plus the
	// deployment's files, minus its deleted paths.
	Merge Mode = "merge"
)

func (m Mode) valid() bool { return m == Replace || m == Merge }

// File is one file to publish. An empty ContentType is derived from the
// path's extension.
type File struct {
	Path, ContentType string
	Body              []byte
}

// Version is an immutable published snapshot of a site.
type Version struct {
	ID          uuid.UUID
	SiteID      string
	PublishedBy uuid.UUID // uuid.Nil once the publisher's account is deleted
	// PublisherEmail is the publisher's primary email, "" when unknown.
	PublisherEmail string
	CreatedAt      time.Time
	FileCount      int
	Size           int64
}

// staged is a file whose blob is stored, ready to record in a deployment.
type staged struct {
	path, contentType string
	sum               [sha256.Size]byte
	size              int64
}

// ErrNotCreator is returned when an account other than a deployment's
// creator adds to, uploads into, or finalizes it: a deployment belongs to
// whoever created it, so another manager cannot slip files into a publish
// that will go out under someone else's name.
var ErrNotCreator = errors.New("sites: deployment was created by another account")

// CreateDeployment opens a staged publish on a site; ErrNotFound when the
// site does not exist. Only by can add to, upload into, or finalize it.
func (s *Service) CreateDeployment(ctx context.Context, siteID string, by uuid.UUID, mode Mode) (uuid.UUID, error) {
	if !mode.valid() {
		return uuid.Nil, inputErrorf("invalid mode %q: want replace or merge", mode)
	}
	id := uuid.Must(uuid.NewV7())
	_, err := db.New(s.db).CreateDeployment(ctx, db.CreateDeploymentParams{
		ID: id, SiteID: siteID, CreatedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}, Mode: string(mode),
	})
	if isForeignKeyViolation(err) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("sites: create deployment: %w", err)
	}
	return id, nil
}

// openDeployment returns an open deployment created by by, ErrNotFound,
// ErrNotCreator, or ErrDeploymentClosed.
func (s *Service) openDeployment(ctx context.Context, id, by uuid.UUID) (db.Deployment, error) {
	d, err := db.New(s.db).GetDeployment(ctx, id)
	if err != nil {
		return db.Deployment{}, notFound(err)
	}
	if !isCreator(d.CreatedBy, by) {
		return db.Deployment{}, ErrNotCreator
	}
	if d.State != "open" {
		return db.Deployment{}, ErrDeploymentClosed
	}
	return d, nil
}

// isCreator reports whether user created a deployment; nobody did once the
// creator's account is deleted.
func isCreator(createdBy uuid.NullUUID, user uuid.UUID) bool {
	return createdBy.Valid && createdBy.UUID == user
}

// Deployment is a staged publish's state.
type Deployment struct {
	ID        uuid.UUID
	SiteID    string
	Mode      Mode
	State     string // "open" | "finalized" | "abandoned"
	CreatedBy uuid.NullUUID
	CreatedAt time.Time
}

// IsCreator reports whether user created the deployment, the only account
// that may add to, upload into, or finalize it.
func (d Deployment) IsCreator(user uuid.UUID) bool { return isCreator(d.CreatedBy, user) }

// GetDeployment returns a deployment in any state, or ErrNotFound.
func (s *Service) GetDeployment(ctx context.Context, id uuid.UUID) (Deployment, error) {
	d, err := db.New(s.db).GetDeployment(ctx, id)
	if err != nil {
		return Deployment{}, notFound(err)
	}
	return Deployment{ID: d.ID, SiteID: d.SiteID, Mode: Mode(d.Mode), State: d.State, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt}, nil
}

// AddFiles stores the files' blobs and adds them to an open deployment
// created by by, replacing earlier additions or deletions of the same
// paths. Within one call a repeated path keeps its last occurrence.
func (s *Service) AddFiles(ctx context.Context, deployment, by uuid.UUID, files []File) error {
	d, err := s.openDeployment(ctx, deployment, by)
	if err != nil {
		return err
	}
	out := make([]staged, 0, len(files))
	for _, f := range files {
		p, err := CleanPath(f.Path)
		if err != nil {
			return err
		}
		ct, err := contentType(p, f.ContentType)
		if err != nil {
			return err
		}
		if int64(len(f.Body)) > MaxFileSize {
			return inputErrorf("file %q is larger than %d bytes", p, MaxFileSize)
		}
		st := staged{path: p, contentType: ct, sum: sha256.Sum256(f.Body), size: int64(len(f.Body))}
		if err := s.putBlob(ctx, d.SiteID, st.sum, st.size, func() (io.Reader, error) { return bytes.NewReader(f.Body), nil }); err != nil {
			return err
		}
		out = append(out, st)
	}
	return s.addStaged(ctx, deployment, out)
}

// putBlob uploads a blob unless the site already has it (site_blobs is the
// record; the store cannot list). ErrNotFound when the site is deleted
// meanwhile.
func (s *Service) putBlob(ctx context.Context, siteID string, sum [sha256.Size]byte, size int64, open func() (io.Reader, error)) error {
	q := db.New(s.db)
	exists, err := q.SiteBlobExists(ctx, db.SiteBlobExistsParams{SiteID: siteID, Sha256: sum[:]})
	if err != nil {
		return fmt.Errorf("sites: check blob: %w", err)
	}
	if exists {
		return nil
	}
	r, err := open()
	if err != nil {
		return err
	}
	// Blobs are shared by paths with different types; the file server sets
	// Content-Type from version_files, never from the object.
	if err := s.store.Put(ctx, blobKey(siteID, sum[:]), r, size, "application/octet-stream"); err != nil {
		return fmt.Errorf("sites: store blob: %w", err)
	}
	err = q.CreateSiteBlob(ctx, db.CreateSiteBlobParams{SiteID: siteID, Sha256: sum[:], Size: size})
	if isForeignKeyViolation(err) {
		// The site was deleted since the check. Delete commits DeleteSite,
		// then clears the site's storage prefix: an object written before
		// that clear is gone with it, and one written after it (this case)
		// can only follow the committed delete, so this insert sees no site
		// and the object is removed here. Either way none is left behind.
		if err := s.store.Delete(context.WithoutCancel(ctx), blobKey(siteID, sum[:])); err != nil {
			s.logger.Error("sites: remove blob of a deleted site", "site", siteID, "err", err)
		}
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sites: record blob: %w", err)
	}
	return nil
}

// addStaged records stored files in a deployment, last occurrence of a path
// winning, while holding a share lock that excludes a concurrent finalize.
func (s *Service) addStaged(ctx context.Context, deployment uuid.UUID, files []staged) error {
	if len(files) == 0 {
		return nil
	}
	last := make(map[string]int, len(files))
	for i, f := range files {
		last[f.path] = i
	}
	p := db.AddDeploymentFilesParams{DeploymentID: deployment}
	for i, f := range files {
		if last[f.path] != i {
			continue
		}
		p.Paths = append(p.Paths, f.path)
		p.Blobs = append(p.Blobs, f.sum[:])
		p.Sizes = append(p.Sizes, f.size)
		p.ContentTypes = append(p.ContentTypes, f.contentType)
	}
	return db.InTx(ctx, s.db, func(q *db.Queries) error {
		if err := lockOpen(ctx, q, deployment); err != nil {
			return err
		}
		if err := q.AddDeploymentFiles(ctx, p); err != nil {
			return fmt.Errorf("sites: add files: %w", err)
		}
		return nil
	})
}

func lockOpen(ctx context.Context, q *db.Queries, deployment uuid.UUID) error {
	d, err := q.ShareLockDeployment(ctx, deployment)
	if err != nil {
		return notFound(err)
	}
	if d.State != "open" {
		return ErrDeploymentClosed
	}
	return nil
}

// DeletePaths records paths to remove from the current version when a
// merge deployment created by by is finalized. Paths the version does not
// have are ignored. Replace deployments reject deletions: they publish only
// what they contain.
func (s *Service) DeletePaths(ctx context.Context, deployment, by uuid.UUID, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	d, err := s.openDeployment(ctx, deployment, by)
	if err != nil {
		return err
	}
	if Mode(d.Mode) != Merge {
		return inputErrorf("delete paths apply only to merge deployments")
	}
	seen := make(map[string]bool, len(paths))
	var clean []string
	for _, p := range paths {
		c, err := CleanPath(p)
		if err != nil {
			return err
		}
		if !seen[c] {
			seen[c] = true
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return db.InTx(ctx, s.db, func(q *db.Queries) error {
		if err := lockOpen(ctx, q, deployment); err != nil {
			return err
		}
		if err := q.AddDeploymentDeletions(ctx, db.AddDeploymentDeletionsParams{DeploymentID: deployment, Paths: clean}); err != nil {
			return fmt.Errorf("sites: delete paths: %w", err)
		}
		return nil
	})
}

// Finalize atomically turns an open deployment created by by into a new
// version, published by by, and makes it current. Merge deployments build
// on the site's current version at finalize time: the site row stays
// locked until commit, so concurrent merges apply one after the other and
// never drop each other's files. A deployment finalizes at most once; one
// with no files or deletions, or whose result would have no files, is
// rejected and stays open.
func (s *Service) Finalize(ctx context.Context, deployment, by uuid.UUID) (Version, error) {
	var out Version
	err := db.InTx(ctx, s.db, func(q *db.Queries) error {
		d, err := q.LockDeployment(ctx, deployment)
		if err != nil {
			return notFound(err)
		}
		if !isCreator(d.CreatedBy, by) {
			return ErrNotCreator
		}
		if d.State != "open" {
			return ErrDeploymentClosed
		}
		n, err := q.CountDeploymentFiles(ctx, deployment)
		if err != nil {
			return err
		}
		if n == 0 {
			return inputErrorf("deployment has no files")
		}
		site, err := q.GetSiteForUpdate(ctx, d.SiteID)
		if err != nil {
			return notFound(err)
		}
		var base uuid.NullUUID
		if Mode(d.Mode) == Merge {
			base = site.CurrentVersionID
		}
		v, err := q.CreateVersion(ctx, db.CreateVersionParams{ID: uuid.Must(uuid.NewV7()), SiteID: d.SiteID, PublishedBy: uuid.NullUUID{UUID: by, Valid: true}})
		if err != nil {
			return err
		}
		files, err := q.CreateVersionFilesFromDeployment(ctx, db.CreateVersionFilesFromDeploymentParams{
			VersionID: v.ID, BaseVersionID: base, DeploymentID: deployment,
		})
		if err != nil {
			return err
		}
		if files == 0 {
			return inputErrorf("the new version would have no files")
		}
		vid := uuid.NullUUID{UUID: v.ID, Valid: true}
		if _, err := q.FinalizeDeployment(ctx, db.FinalizeDeploymentParams{ID: deployment, VersionID: vid}); err != nil {
			return err
		}
		if _, err := q.SetCurrentVersion(ctx, db.SetCurrentVersionParams{ID: d.SiteID, VersionID: vid}); err != nil {
			return err
		}
		row, err := q.GetVersion(ctx, db.GetVersionParams{ID: v.ID, SiteID: d.SiteID})
		if err != nil {
			return err
		}
		out = versionFromRow(db.ListVersionsRow(row))
		return nil
	})
	var ie *InputError
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNotCreator) && !errors.Is(err, ErrDeploymentClosed) && !errors.As(err, &ie) {
		return Version{}, fmt.Errorf("sites: finalize: %w", err)
	}
	return out, err
}

// Publish is the one-shot form: one deployment with files and (merge mode)
// deleted paths, finalized at once. On failure the deployment is abandoned.
func (s *Service) Publish(ctx context.Context, siteID string, by uuid.UUID, mode Mode, files []File, deletePaths []string) (Version, error) {
	if mode == Replace && len(deletePaths) > 0 {
		return Version{}, inputErrorf("delete paths apply only to merge deployments")
	}
	dep, err := s.CreateDeployment(ctx, siteID, by, mode)
	if err != nil {
		return Version{}, err
	}
	v, err := func() (Version, error) {
		if err := s.AddFiles(ctx, dep, by, files); err != nil {
			return Version{}, err
		}
		if err := s.DeletePaths(ctx, dep, by, deletePaths); err != nil {
			return Version{}, err
		}
		return s.Finalize(ctx, dep, by)
	}()
	if err != nil {
		// Best effort: an open leftover is harmless and collected by GC.
		_ = db.New(s.db).AbandonDeployment(context.WithoutCancel(ctx), dep)
		return Version{}, err
	}
	return v, nil
}

func blobKey(siteID string, sum []byte) string {
	return sitePrefix(siteID) + "blobs/" + hex.EncodeToString(sum)
}

func versionFromRow(r db.ListVersionsRow) Version {
	v := Version{
		ID:          r.ID,
		SiteID:      r.SiteID,
		PublishedBy: r.PublishedBy.UUID,
		CreatedAt:   r.CreatedAt,
		FileCount:   int(r.FileCount),
		Size:        r.Size,
	}
	if r.PublisherEmail != nil {
		v.PublisherEmail = *r.PublisherEmail
	}
	return v
}
