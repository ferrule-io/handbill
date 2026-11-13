// Package storage defines the object storage adapter. Implementations
// (local filesystem, S3, GCS) live in subpackages and share one contract
// test suite; see docs/workplan.md.
package storage

import (
	"github.com/mjquinlan2000/agent-dash/internal/storage/storagetype"
)

// ErrNotFound is returned (wrapped or bare) when a key does not exist.
var ErrNotFound = storagetype.ErrNotFound

// ErrInvalidKey is returned when a key fails validation.
var ErrInvalidKey = storagetype.ErrInvalidKey

// Store is a flat key/value object store. Keys are slash-separated relative
// paths without a leading slash, "." or ".." segments; adapters apply their
// configured prefix. Objects are written once and never modified in place.
type Store = storagetype.Store

// Info is object metadata.
type Info = storagetype.Info

// Object is an open object.
type Object = storagetype.Object

// ValidKey checks that key is relative, has no empty/"."/".." segments,
// no backslash, no leading slash, and no NUL.
func ValidKey(key string) error {
	return storagetype.ValidKey(key)
}

// ValidPrefix checks that prefix is non-empty, relative, and follows key
// segment rules (allowing an optional trailing slash).
func ValidPrefix(prefix string) error {
	return storagetype.ValidPrefix(prefix)
}
