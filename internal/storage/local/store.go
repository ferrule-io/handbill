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

	"github.com/mjquinlan2000/agent-dash/internal/storage/storagetype"
)

// Store implements storagetype.Store on the local filesystem.
type Store struct {
	rootPath string
	rootFS   *os.Root
}

type metadata struct {
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// New opens or creates a local store at root.
func New(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("local: abs path: %w", err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("local: mkdir root: %w", err)
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

func metaKey(key string) string {
	return key + ".meta"
}

func randomSuffix() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// Put writes an object atomically: writes to temp files in the same directory,
// fsyncs, and renames.
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

	dir := filepath.Dir(key)
	base := filepath.Base(key)
	if dir != "." {
		if err := s.rootFS.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("local: mkdir %q: %w", dir, err)
		}
	}

	randPart := randomSuffix()
	tmpDataName := filepath.Join(dir, fmt.Sprintf(".tmp-%s-data-%s", base, randPart))
	tmpMetaName := filepath.Join(dir, fmt.Sprintf(".tmp-%s-meta-%s", base, randPart))

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
			return fmt.Errorf("local: short write: wrote %d of %d bytes", written, size)
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
	tmpData = nil // prevented cleanup remove of data

	// Write sidecar metadata
	meta := metadata{
		ContentType: contentType,
		Size:        written,
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

	// Rename sidecar then data
	if err := s.rootFS.Rename(tmpMetaName, metaKey(key)); err != nil {
		_ = s.rootFS.Remove(tmpDataName)
		_ = s.rootFS.Remove(tmpMetaName)
		return fmt.Errorf("local: rename meta: %w", err)
	}
	if err := s.rootFS.Rename(tmpDataName, key); err != nil {
		_ = s.rootFS.Remove(tmpDataName)
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

	f, err := s.rootFS.Open(key)
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

	meta := s.readMeta(key, fi.Size())

	return &storagetype.Object{
		Info: storagetype.Info{
			Size:        meta.Size,
			ContentType: meta.ContentType,
		},
		Body: f,
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

	fi, err := s.rootFS.Stat(key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist) {
			return storagetype.Info{}, storagetype.ErrNotFound
		}
		return storagetype.Info{}, err
	}
	if fi.IsDir() {
		return storagetype.Info{}, storagetype.ErrNotFound
	}

	meta := s.readMeta(key, fi.Size())
	return storagetype.Info{
		Size:        meta.Size,
		ContentType: meta.ContentType,
	}, nil
}

func (s *Store) readMeta(key string, fallbackSize int64) metadata {
	meta := metadata{
		ContentType: "application/octet-stream",
		Size:        fallbackSize,
	}
	data, err := s.rootFS.ReadFile(metaKey(key))
	if err == nil {
		var m metadata
		if err := json.Unmarshal(data, &m); err == nil {
			if m.ContentType != "" {
				meta.ContentType = m.ContentType
			}
			if m.Size >= 0 {
				meta.Size = m.Size
			}
		}
	}
	return meta
}

// Delete removes an object and its sidecar metadata.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	_ = s.rootFS.Remove(key)
	_ = s.rootFS.Remove(metaKey(key))
	return nil
}

// DeletePrefix removes every object whose key starts with prefix.
func (s *Store) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cleanPrefix := filepath.ToSlash(prefix)

	// If prefix is empty, remove all contents of root
	if cleanPrefix == "" {
		entries, err := os.ReadDir(s.rootPath)
		if err != nil {
			return fmt.Errorf("local: read root: %w", err)
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(s.rootPath, e.Name())); err != nil {
				return fmt.Errorf("local: remove root entry %q: %w", e.Name(), err)
			}
		}
		return nil
	}

	// Walk from rootPath and remove matching files and directories
	var pathsToRemove []string
	err := filepath.WalkDir(s.rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if path == s.rootPath {
			return nil
		}
		rel, err := filepath.Rel(s.rootPath, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		// Check if relSlash or without .meta suffix starts with cleanPrefix
		targetKey := strings.TrimSuffix(relSlash, ".meta")
		if strings.HasPrefix(relSlash, cleanPrefix) || strings.HasPrefix(targetKey, cleanPrefix) {
			pathsToRemove = append(pathsToRemove, path)
			if d.IsDir() {
				return fs.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("local: walk for delete prefix: %w", err)
	}

	// Remove collected paths
	for _, p := range pathsToRemove {
		_ = os.RemoveAll(p)
	}

	// Clean up any empty parent directories under rootPath
	_ = filepath.WalkDir(s.rootPath, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != s.rootPath {
			_ = os.Remove(path) // Only removes if empty
		}
		return nil
	})

	return nil
}

// Ping verifies the storage root directory is writable.
func (s *Store) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.rootPath, ".ping-*")
	if err != nil {
		return fmt.Errorf("local: ping write check failed: %w", err)
	}
	tmpName := f.Name()
	_ = f.Close()
	_ = os.Remove(tmpName)
	return nil
}
