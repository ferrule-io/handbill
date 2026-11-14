// Package local implements the local filesystem storage adapter.
package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ferrule-io/handbill/internal/storage/storagetype"
)

// Store implements storagetype.Store on the local filesystem.
type Store struct {
	rootPath string
	rootFS   *os.Root
}

type metadata struct {
	ContentType string `json:"content_type"`
}

// New opens or creates a local store at root.
func New(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("local: abs path: %w", err)
	}
	for _, sub := range []string{"objects", "meta", "tmp"} {
		if err := os.MkdirAll(filepath.Join(abs, sub), 0755); err != nil {
			return nil, fmt.Errorf("local: mkdir %s: %w", sub, err)
		}
	}
	rootFS, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("local: open root: %w", err)
	}
	return &Store{
		rootPath: abs,
		rootFS:   rootFS,
	}, nil
}

// Close closes the underlying root file descriptor.
func (s *Store) Close() error {
	if s.rootFS != nil {
		return s.rootFS.Close()
	}
	return nil
}

func randomSuffix() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

func objectRelPath(key string) string {
	return filepath.Join("objects", filepath.FromSlash(key))
}

func metaRelPath(key string) string {
	return filepath.Join("meta", filepath.FromSlash(key))
}

// Put writes an object atomically: writes to temp files in tmp/, fsyncs,
// and renames into objects/ and meta/.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	objRel := objectRelPath(key)
	metaRel := metaRelPath(key)

	if dir := filepath.Dir(objRel); dir != "objects" {
		if err := s.rootFS.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("local: mkdir objects: %w", err)
		}
	}
	if dir := filepath.Dir(metaRel); dir != "meta" {
		if err := s.rootFS.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("local: mkdir meta: %w", err)
		}
	}

	randPart := randomSuffix()
	tmpDataName := filepath.Join("tmp", "data-"+randPart)
	tmpMetaName := filepath.Join("tmp", "meta-"+randPart)

	tmpData, err := s.rootFS.OpenFile(tmpDataName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("local: create temp data: %w", err)
	}
	defer func() {
		if tmpData != nil {
			_ = tmpData.Close()
			_ = s.rootFS.Remove(tmpDataName)
		}
	}()

	var written int64
	if size >= 0 {
		written, err = io.CopyN(tmpData, r, size)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("local: write data: %w", err)
		}
		if written != size {
			return fmt.Errorf("local: short read: read %d of %d bytes", written, size)
		}
	} else {
		written, err = io.Copy(tmpData, r)
		if err != nil {
			return fmt.Errorf("local: write data: %w", err)
		}
	}

	if err := tmpData.Sync(); err != nil {
		return fmt.Errorf("local: fsync data: %w", err)
	}
	if err := tmpData.Close(); err != nil {
		return fmt.Errorf("local: close data: %w", err)
	}
	tmpData = nil

	// Write metadata file
	meta := metadata{
		ContentType: contentType,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		return fmt.Errorf("local: marshal metadata: %w", err)
	}

	tmpMeta, err := s.rootFS.OpenFile(tmpMetaName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		return fmt.Errorf("local: create temp meta: %w", err)
	}
	defer func() {
		if tmpMeta != nil {
			_ = tmpMeta.Close()
			_ = s.rootFS.Remove(tmpMetaName)
		}
	}()

	if _, err := tmpMeta.Write(metaBytes); err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		return fmt.Errorf("local: write meta: %w", err)
	}
	if err := tmpMeta.Sync(); err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		return fmt.Errorf("local: fsync meta: %w", err)
	}
	if err := tmpMeta.Close(); err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		return fmt.Errorf("local: close meta: %w", err)
	}
	tmpMeta = nil

	// Rename metadata then data atomically
	if err := s.rootFS.Rename(tmpMetaName, metaRel); err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		_ = s.rootFS.Remove(tmpMetaName)
		return fmt.Errorf("local: rename meta: %w", err)
	}
	if err := s.rootFS.Rename(tmpDataName, objRel); err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		_ = s.rootFS.Remove(metaRel)
		return fmt.Errorf("local: rename data: %w", err)
	}

	return nil
}

// Get opens an object for reading.
func (s *Store) Get(ctx context.Context, key string) (*storagetype.Object, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := s.rootFS.Open(objectRelPath(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist) {
			return nil, storagetype.ErrNotFound
		}
		return nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if fi.IsDir() {
		_ = f.Close()
		return nil, storagetype.ErrNotFound
	}

	contentType := s.readContentType(key)

	return &storagetype.Object{
		Info: storagetype.Info{
			Size:        fi.Size(),
			ContentType: contentType,
		},
		Body: f,
	}, nil
}

// readCloser reads from Reader and closes Closer.
type readCloser struct {
	io.Reader
	io.Closer
}

// GetRange opens length bytes of an object from offset.
func (s *Store) GetRange(ctx context.Context, key string, offset, length int64) (*storagetype.Object, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := s.rootFS.Open(objectRelPath(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist) {
			return nil, storagetype.ErrNotFound
		}
		return nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if fi.IsDir() {
		_ = f.Close()
		return nil, storagetype.ErrNotFound
	}
	if offset < 0 || length <= 0 || offset+length > fi.Size() {
		_ = f.Close()
		return nil, fmt.Errorf("local: range %d+%d outside object of %d bytes", offset, length, fi.Size())
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}

	return &storagetype.Object{
		Info: storagetype.Info{
			Size:        length,
			ContentType: s.readContentType(key),
		},
		Body: readCloser{io.LimitReader(f, length), f},
	}, nil
}

// Stat returns metadata without the body.
func (s *Store) Stat(ctx context.Context, key string) (storagetype.Info, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return storagetype.Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return storagetype.Info{}, err
	}

	fi, err := s.rootFS.Stat(objectRelPath(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist) {
			return storagetype.Info{}, storagetype.ErrNotFound
		}
		return storagetype.Info{}, err
	}
	if fi.IsDir() {
		return storagetype.Info{}, storagetype.ErrNotFound
	}

	contentType := s.readContentType(key)
	return storagetype.Info{
		Size:        fi.Size(),
		ContentType: contentType,
	}, nil
}

func (s *Store) readContentType(key string) string {
	contentType := "application/octet-stream"
	data, err := s.rootFS.ReadFile(metaRelPath(key))
	if err == nil {
		var m metadata
		if err := json.Unmarshal(data, &m); err == nil && m.ContentType != "" {
			contentType = m.ContentType
		}
	}
	return contentType
}

// Delete removes an object and its metadata.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	_ = s.rootFS.Remove(objectRelPath(key))
	_ = s.rootFS.Remove(metaRelPath(key))
	return nil
}

// DeletePrefix removes every object whose key starts with prefix.
func (s *Store) DeletePrefix(ctx context.Context, prefix string) error {
	if err := storagetype.ValidPrefix(prefix); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, sub := range []string{"objects", "meta"} {
		if err := s.deletePrefixSubdir(sub, prefix); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deletePrefixSubdir(sub, prefix string) error {
	cleanPrefix := filepath.ToSlash(prefix)
	subDir := filepath.Join(s.rootPath, sub)

	if strings.HasSuffix(cleanPrefix, "/") {
		// Target is an exact directory subtree
		dirPath := filepath.Join(subDir, filepath.FromSlash(strings.TrimSuffix(cleanPrefix, "/")))
		_ = os.RemoveAll(dirPath)
		s.pruneAncestors(subDir, filepath.Dir(dirPath))
		return nil
	}

	// Target may be a directory or file prefix in parent dir
	parentDir := filepath.Dir(cleanPrefix)
	basePrefix := filepath.Base(cleanPrefix)
	var searchDir string
	if parentDir == "." {
		searchDir = subDir
	} else {
		searchDir = filepath.Join(subDir, filepath.FromSlash(parentDir))
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("local: read dir %q: %w", searchDir, err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), basePrefix) {
			target := filepath.Join(searchDir, entry.Name())
			_ = os.RemoveAll(target)
		}
	}

	s.pruneAncestors(subDir, searchDir)
	return nil
}

func (s *Store) pruneAncestors(baseDir, startDir string) {
	curr := startDir
	for {
		if curr == baseDir || !strings.HasPrefix(curr, baseDir) {
			break
		}
		// Attempt to remove directory (only succeeds if empty)
		if err := os.Remove(curr); err != nil {
			break
		}
		curr = filepath.Dir(curr)
	}
}

// Ping verifies the storage root directory is writable.
func (s *Store) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmpName := filepath.Join(s.rootPath, "tmp", ".ping-"+randomSuffix())
	f, err := os.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("local: ping write check failed: %w", err)
	}
	_ = f.Close()
	_ = os.Remove(tmpName)
	return nil
}
