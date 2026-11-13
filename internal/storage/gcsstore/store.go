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
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/storage/storagetype"
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

// Put writes an object to GCS.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := storagetype.ValidKey(key); err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	obj := s.bucket.Object(s.gcsKey(key))
	w := obj.NewWriter(ctx)
	w.ContentType = contentType

	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return fmt.Errorf("gcsstore: write object: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcsstore: close writer: %w", err)
	}
	return nil
}

// Get opens an object from GCS for reading.
func (s *Store) Get(ctx context.Context, key string) (*storagetype.Object, error) {
	if err := storagetype.ValidKey(key); err != nil {
		return nil, err
	}

	obj := s.bucket.Object(s.gcsKey(key))
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, storagetype.ErrNotFound
		}
		return nil, fmt.Errorf("gcsstore: attrs: %w", err)
	}

	r, err := obj.NewReader(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, storagetype.ErrNotFound
		}
		return nil, fmt.Errorf("gcsstore: reader: %w", err)
	}

	return &storagetype.Object{
		Info: storagetype.Info{
			Size:        attrs.Size,
			ContentType: attrs.ContentType,
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
	fullPrefix := prefix
	if s.prefix != "" {
		if prefix == "" {
			fullPrefix = s.prefix + "/"
		} else {
			fullPrefix = s.prefix + "/" + prefix
		}
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

// Ping checks that the GCS bucket exists and is accessible.
func (s *Store) Ping(ctx context.Context) error {
	_, err := s.bucket.Attrs(ctx)
	if err != nil {
		return fmt.Errorf("gcsstore: ping bucket %q: %w", s.name, err)
	}
	return nil
}

func isGCSNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gcs.ErrObjectNotExist) {
		return true
	}
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) && googleErr.Code == 404 {
		return true
	}
	return false
}
