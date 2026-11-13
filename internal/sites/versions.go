package sites

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/siteid"
	"github.com/ferrule-io/handbill/internal/storage"
)

// ListVersions returns every version of a site, newest first.
func (s *Service) ListVersions(ctx context.Context, siteID string) ([]Version, error) {
	rows, err := db.New(s.db).ListVersions(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("sites: list versions: %w", err)
	}
	out := make([]Version, len(rows))
	for i, r := range rows {
		out[i] = versionFromRow(r)
	}
	return out, nil
}

// GetVersion returns one version of a site, or ErrNotFound.
func (s *Service) GetVersion(ctx context.Context, siteID string, version uuid.UUID) (Version, error) {
	r, err := db.New(s.db).GetVersion(ctx, db.GetVersionParams{ID: version, SiteID: siteID})
	if err != nil {
		return Version{}, notFound(err)
	}
	return versionFromRow(db.ListVersionsRow(r)), nil
}

// Rollback makes an existing version of the site current again (no copy);
// ErrNotFound when the site or that version of it does not exist.
func (s *Service) Rollback(ctx context.Context, siteID string, version uuid.UUID) error {
	n, err := db.New(s.db).RollbackSite(ctx, db.RollbackSiteParams{SiteID: siteID, VersionID: version})
	if err != nil {
		return fmt.Errorf("sites: rollback: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// FileInfo describes one file of a version. SHA256 is the blob's hex digest.
type FileInfo struct {
	Path, ContentType string
	Size              int64
	SHA256            string
}

// ListFiles returns the current version's files sorted by path; none when
// nothing is published. ErrNotFound when the site does not exist.
func (s *Service) ListFiles(ctx context.Context, siteID string) ([]FileInfo, error) {
	site, err := s.Get(ctx, siteID)
	if err != nil {
		return nil, err
	}
	if !site.CurrentVersionID.Valid {
		return nil, nil
	}
	rows, err := db.New(s.db).ListVersionFiles(ctx, site.CurrentVersionID.UUID)
	if err != nil {
		return nil, fmt.Errorf("sites: list files: %w", err)
	}
	out := make([]FileInfo, len(rows))
	for i, r := range rows {
		out[i] = FileInfo{Path: r.Path, ContentType: r.ContentType, Size: r.Size, SHA256: hex.EncodeToString(r.BlobSha256)}
	}
	return out, nil
}

// OpenFile opens one file of the current version; ErrNotFound when the
// site, a current version, or the file does not exist. The caller closes
// the object's Body.
func (s *Service) OpenFile(ctx context.Context, siteID, path string) (*storage.Object, FileInfo, error) {
	site, err := s.Get(ctx, siteID)
	if err != nil {
		return nil, FileInfo{}, err
	}
	if !site.CurrentVersionID.Valid {
		return nil, FileInfo{}, ErrNotFound
	}
	p, err := CleanPath(path)
	if err != nil {
		return nil, FileInfo{}, err
	}
	fi, err := s.FindFile(ctx, site.CurrentVersionID.UUID, p)
	if err != nil {
		return nil, FileInfo{}, err
	}
	obj, err := s.OpenBlob(ctx, siteID, fi.SHA256)
	if err != nil {
		return nil, FileInfo{}, err
	}
	return obj, fi, nil
}

// FindFile returns the first of candidates (site paths, tried in order)
// present in a version, or ErrNotFound. Invalid candidates never match.
// The file server resolves against the version Access reported, so one
// response never mixes versions.
func (s *Service) FindFile(ctx context.Context, version uuid.UUID, candidates ...string) (FileInfo, error) {
	paths := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if p, err := CleanPath(c); err == nil && p == c {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return FileInfo{}, ErrNotFound
	}
	r, err := db.New(s.db).FindVersionFile(ctx, db.FindVersionFileParams{VersionID: version, Paths: paths})
	if err != nil {
		return FileInfo{}, notFound(err)
	}
	return FileInfo{Path: r.Path, ContentType: r.ContentType, Size: r.Size, SHA256: hex.EncodeToString(r.BlobSha256)}, nil
}

// OpenBlob opens a site's blob by hex SHA-256; ErrNotFound when missing.
func (s *Service) OpenBlob(ctx context.Context, siteID, sha256Hex string) (*storage.Object, error) {
	sum, err := hex.DecodeString(sha256Hex)
	if err != nil || len(sum) != 32 || !siteid.Valid(siteID) {
		return nil, ErrNotFound
	}
	obj, err := s.store.Get(ctx, blobKey(siteID, sum))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sites: open blob: %w", err)
	}
	return obj, nil
}
