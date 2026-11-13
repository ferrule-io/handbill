package storagetype

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrNotFound is returned (wrapped or bare) when a key does not exist.
var ErrNotFound = errors.New("storage: object not found")

// ErrInvalidKey is returned when a key fails validation.
var ErrInvalidKey = errors.New("storage: invalid key")

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

// ValidKey validates that key is a relative path without leading slash,
// backslashes, empty segments, "." or ".." segments, or NUL bytes.
func ValidKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: key is empty", ErrInvalidKey)
	}
	if strings.ContainsRune(key, 0) {
		return fmt.Errorf("%w: key contains NUL byte", ErrInvalidKey)
	}
	if strings.Contains(key, "\\") {
		return fmt.Errorf("%w: key contains backslash", ErrInvalidKey)
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("%w: key has leading slash", ErrInvalidKey)
	}
	parts := strings.Split(key, "/")
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("%w: key contains empty segment", ErrInvalidKey)
		}
		if part == "." {
			return fmt.Errorf("%w: key contains '.' segment", ErrInvalidKey)
		}
		if part == ".." {
			return fmt.Errorf("%w: key contains '..' segment", ErrInvalidKey)
		}
	}
	return nil
}

// ValidPrefix validates that prefix is non-empty, relative, and follows key
// segment rules (allowing an optional trailing slash).
func ValidPrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("%w: prefix is empty", ErrInvalidKey)
	}
	if strings.ContainsRune(prefix, 0) {
		return fmt.Errorf("%w: prefix contains NUL byte", ErrInvalidKey)
	}
	if strings.Contains(prefix, "\\") {
		return fmt.Errorf("%w: prefix contains backslash", ErrInvalidKey)
	}
	if strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("%w: prefix has leading slash", ErrInvalidKey)
	}
	p := strings.TrimSuffix(prefix, "/")
	if p == "" {
		return fmt.Errorf("%w: prefix has empty segments", ErrInvalidKey)
	}
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("%w: prefix contains empty segment", ErrInvalidKey)
		}
		if part == "." {
			return fmt.Errorf("%w: prefix contains '.' segment", ErrInvalidKey)
		}
		if part == ".." {
			return fmt.Errorf("%w: prefix contains '..' segment", ErrInvalidKey)
		}
	}
	return nil
}
