// Package gcsstore implements the Google Cloud Storage adapter.
package gcsstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/storage/storagetype"
)

// Store implements storagetype.Store on Google Cloud Storage.
type Store struct {
	client *gcs.Client
	bucket *gcs.BucketHandle
	name   string
	prefix string
}

// New creates a GCS store from configuration.
func New(ctx context.Context, cfg config.GCS) (*Store, error) {
	var opts []option.ClientOption
	if os.Getenv("STORAGE_EMULATOR_HOST") != "" {
		opts = append(opts, option.WithoutAuthentication())
	}

	client, err := gcs.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gcsstore: create client: %w", err)
	}

	return &Store{
		client: client,
		bucket: client.Bucket(cfg.Bucket),
		name:   cfg.Bucket,
		prefix: strings.Trim(cfg.Prefix, "/"),
	}, nil
}

// Close closes the underlying GCS client.
func (s *Store) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func (s *Store) gcsKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

// Put writes an object to GCS. It uses a child context that is cancelled on any error
// to avoid committing a partial object.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	wCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	obj := s.bucket.Object(s.gcsKey(key))
	w := obj.NewWriter(wCtx)
	w.ContentType = contentType

	var written int64
	var err error
	if size >= 0 {
		written, err = io.CopyN(w, r, size)
		if err != nil && !errors.Is(err, io.EOF) {
			cancel()
			_ = w.Close()
			return fmt.Errorf("gcsstore: write object: %w", err)
		}
		if written != size {
			cancel()
			_ = w.Close()
			return fmt.Errorf("gcsstore: short write: wrote %d of %d bytes", written, size)
		}
	} else {
		written, err = io.Copy(w, r)
		if err != nil {
			cancel()
			_ = w.Close()
			return fmt.Errorf("gcsstore: write object: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("gcsstore: close writer: %w", err)
	}
	return nil
}

// Get opens an object from GCS for reading and extracts metadata from reader attributes.
func (s *Store) Get(ctx context.Context, key string) (*storagetype.Object, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return nil, err
	}

	obj := s.bucket.Object(s.gcsKey(key))
	r, err := obj.NewReader(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, storagetype.ErrNotFound
		}
		return nil, fmt.Errorf("gcsstore: reader: %w", err)
	}

	attrs := r.Attrs
	return &storagetype.Object{
		Info: storagetype.Info{
			Size:        attrs.Size,
			ContentType: attrs.ContentType,
		},
		Body: r,
	}, nil
}

// GetRange opens length bytes of an object from offset with a range reader.
func (s *Store) GetRange(ctx context.Context, key string, offset, length int64) (*storagetype.Object, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return nil, err
	}

	r, err := s.bucket.Object(s.gcsKey(key)).NewRangeReader(ctx, offset, length)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, storagetype.ErrNotFound
		}
		return nil, fmt.Errorf("gcsstore: range reader: %w", err)
	}
	// GCS truncates a range running past the end instead of failing.
	if r.Remain() != length {
		_ = r.Close()
		return nil, fmt.Errorf("gcsstore: range %d+%d returned %d bytes", offset, length, r.Remain())
	}

	return &storagetype.Object{
		Info: storagetype.Info{
			Size:        length,
			ContentType: r.Attrs.ContentType,
		},
		Body: r,
	}, nil
}

// Stat retrieves object metadata from GCS.
func (s *Store) Stat(ctx context.Context, key string) (storagetype.Info, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return storagetype.Info{}, err
	}

	attrs, err := s.bucket.Object(s.gcsKey(key)).Attrs(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return storagetype.Info{}, storagetype.ErrNotFound
		}
		return storagetype.Info{}, fmt.Errorf("gcsstore: stat: %w", err)
	}

	return storagetype.Info{
		Size:        attrs.Size,
		ContentType: attrs.ContentType,
	}, nil
}

// Delete removes an object from GCS.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}

	err := s.bucket.Object(s.gcsKey(key)).Delete(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil
		}
		return fmt.Errorf("gcsstore: delete: %w", err)
	}
	return nil
}

// DeletePrefix removes every object whose key starts with prefix.
func (s *Store) DeletePrefix(ctx context.Context, prefix string) error {
	if err := storagetype.ValidPrefix(prefix); err != nil {
		return err
	}

	fullPrefix := prefix
	if s.prefix != "" {
		fullPrefix = s.prefix + "/" + prefix
	}

	it := s.bucket.Objects(ctx, &gcs.Query{Prefix: fullPrefix})
	var toDelete []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return fmt.Errorf("gcsstore: list prefix objects: %w", err)
		}
		toDelete = append(toDelete, attrs.Name)
	}

	if len(toDelete) == 0 {
		return nil
	}

	// Concurrently delete objects for speed
	const numWorkers = 20
	jobs := make(chan string, len(toDelete))
	for _, name := range toDelete {
		jobs <- name
	}
	close(jobs)

	errChan := make(chan error, 1)
	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				if err := s.bucket.Object(name).Delete(ctx); err != nil && !isGCSNotFound(err) {
					select {
					case errChan <- err:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errChan)

	if err, ok := <-errChan; ok {
		return fmt.Errorf("gcsstore: delete object: %w", err)
	}
	return nil
}

// Ping checks that the GCS bucket is accessible via a 1-item object query under prefix.
// This requires only object read permission and avoids requiring storage.buckets.get.
func (s *Store) Ping(ctx context.Context) error {
	it := s.bucket.Objects(ctx, &gcs.Query{
		Prefix: s.prefix,
	})
	_, err := it.Next()
	if err != nil && !errors.Is(err, iterator.Done) {
		if isGCSNotFound(err) {
			return nil
		}
		return fmt.Errorf("gcsstore: ping: %w", err)
	}
	return nil
}

func isGCSNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, gcs.ErrObjectNotExist)
}
