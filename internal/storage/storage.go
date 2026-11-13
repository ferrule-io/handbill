// Package storage defines the object storage adapter. Implementations
// (local filesystem, S3, GCS) live in subpackages and share one contract
// test suite; see docs/workplan.md.
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned (wrapped or bare) when a key does not exist.
var ErrNotFound = errors.New("storage: object not found")

// Store is a flat key/value object store. Keys are slash-separated relative
// paths without a leading slash, "." or ".." segments; adapters apply their
// configured prefix. Objects are written once and never modified in place.
type Store interface {
	// Put writes an object, replacing any existing object at key. size is
	// the exact length of r. The object is visible only once Put returns nil.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens an object for reading. The caller closes Object.Body.
	Get(ctx context.Context, key string) (*Object, error)
	// Stat returns an object's metadata without its body.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes an object. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// DeletePrefix removes every object whose key starts with prefix.
	DeletePrefix(ctx context.Context, prefix string) error
	// Ping checks that the backend is reachable and the bucket or directory
	// is usable; it backs /readyz.
	Ping(ctx context.Context) error
}

// Info is object metadata.
type Info struct {
	Size        int64
	ContentType string
}

// Object is an open object.
type Object struct {
	Info
	Body io.ReadCloser
}
