// Package storagetest provides the shared contract test suite that all
// storage adapters (local, S3, GCS) must satisfy.
package storagetest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"

	"github.com/ferrule-io/handbill/internal/storage"
)

type onlyReader struct {
	r io.Reader
}

func (o onlyReader) Read(p []byte) (int, error) {
	return o.r.Read(p)
}

// Run runs the contract test suite against the store returned by newStore.
// newStore should return a store scoped with an isolated prefix or directory
// so tests do not interfere with each other.
func Run(t *testing.T, newStore func(t *testing.T) storage.Store) {
	t.Run("Ping", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Ping(ctx); err != nil {
			t.Fatalf("Ping failed: %v", err)
		}
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if err := s.Ping(canceledCtx); err == nil {
			t.Fatalf("Ping with canceled context should have failed")
		}
	})

	t.Run("RoundTrip", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		key := "test/file.txt"
		content := "hello storage world"
		contentType := "text/plain; charset=utf-8"

		if err := s.Put(ctx, key, strings.NewReader(content), int64(len(content)), contentType); err != nil {
			t.Fatalf("Put failed: %v", err)
		}

		info, err := s.Stat(ctx, key)
		if err != nil {
			t.Fatalf("Stat failed: %v", err)
		}
		if info.Size != int64(len(content)) {
			t.Errorf("Stat size = %d, want %d", info.Size, len(content))
		}
		if info.ContentType != contentType {
			t.Errorf("Stat contentType = %q, want %q", info.ContentType, contentType)
		}

		obj, err := s.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer obj.Body.Close()

		data, err := io.ReadAll(obj.Body)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if string(data) != content {
			t.Errorf("Get content = %q, want %q", string(data), content)
		}
		if obj.Size != int64(len(content)) {
			t.Errorf("Get obj.Size = %d, want %d", obj.Size, len(content))
		}
		if obj.ContentType != contentType {
			t.Errorf("Get obj.ContentType = %q, want %q", obj.ContentType, contentType)
		}
	})

	t.Run("NonSeekableReader", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		key := "stream/archive-entry.bin"
		content := "streaming content from non-seekable reader"
		contentType := "application/octet-stream"

		reader := onlyReader{r: strings.NewReader(content)}
		if err := s.Put(ctx, key, reader, int64(len(content)), contentType); err != nil {
			t.Fatalf("Put non-seekable failed: %v", err)
		}

		obj, err := s.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get non-seekable failed: %v", err)
		}
		defer obj.Body.Close()

		data, err := io.ReadAll(obj.Body)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if string(data) != content {
			t.Errorf("Get content = %q, want %q", string(data), content)
		}
	})

	t.Run("ShortReader", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		key := "aborted/short.bin"

		// Size is declared as 100 bytes, but reader only supplies 20 bytes
		shortContent := "12345678901234567890"
		reader := onlyReader{r: strings.NewReader(shortContent)}
		err := s.Put(ctx, key, reader, 100, "application/octet-stream")
		if err == nil {
			t.Fatalf("Put with short reader should fail, but got nil")
		}

		// Must leave no object behind
		_, err = s.Stat(ctx, key)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Stat on short-written key should return ErrNotFound, got %v", err)
		}
		_, err = s.Get(ctx, key)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Get on short-written key should return ErrNotFound, got %v", err)
		}
	})

	t.Run("MetaKeyCoexist", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()

		keyA := "docs/a"
		contentA := "hello a"
		typeA := "text/plain"

		keyAMeta := "docs/a.meta"
		contentAMeta := "hello a.meta"
		typeAMeta := "application/json"

		if err := s.Put(ctx, keyA, strings.NewReader(contentA), int64(len(contentA)), typeA); err != nil {
			t.Fatalf("Put %q failed: %v", keyA, err)
		}
		if err := s.Put(ctx, keyAMeta, strings.NewReader(contentAMeta), int64(len(contentAMeta)), typeAMeta); err != nil {
			t.Fatalf("Put %q failed: %v", keyAMeta, err)
		}

		objA, err := s.Get(ctx, keyA)
		if err != nil {
			t.Fatalf("Get %q failed: %v", keyA, err)
		}
		dataA, _ := io.ReadAll(objA.Body)
		objA.Body.Close()
		if string(dataA) != contentA {
			t.Errorf("%q content = %q, want %q", keyA, string(dataA), contentA)
		}
		if objA.ContentType != typeA {
			t.Errorf("%q contentType = %q, want %q", keyA, objA.ContentType, typeA)
		}

		objAMeta, err := s.Get(ctx, keyAMeta)
		if err != nil {
			t.Fatalf("Get %q failed: %v", keyAMeta, err)
		}
		dataAMeta, _ := io.ReadAll(objAMeta.Body)
		objAMeta.Body.Close()
		if string(dataAMeta) != contentAMeta {
			t.Errorf("%q content = %q, want %q", keyAMeta, string(dataAMeta), contentAMeta)
		}
		if objAMeta.ContentType != typeAMeta {
			t.Errorf("%q contentType = %q, want %q", keyAMeta, objAMeta.ContentType, typeAMeta)
		}

		// Deleting keyA must leave keyAMeta intact
		if err := s.Delete(ctx, keyA); err != nil {
			t.Fatalf("Delete %q failed: %v", keyA, err)
		}
		if _, err := s.Get(ctx, keyA); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("%q should be not found after delete", keyA)
		}
		objAMetaAfter, err := s.Get(ctx, keyAMeta)
		if err != nil {
			t.Fatalf("%q was corrupted or deleted after deleting %q: %v", keyAMeta, keyA, err)
		}
		objAMetaAfter.Body.Close()
	})

	t.Run("Overwrite", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		key := "overwrite.bin"

		if err := s.Put(ctx, key, strings.NewReader("v1"), 2, "text/plain"); err != nil {
			t.Fatalf("Put v1 failed: %v", err)
		}
		if err := s.Put(ctx, key, strings.NewReader("v2-new-content"), 14, "application/octet-stream"); err != nil {
			t.Fatalf("Put v2 failed: %v", err)
		}

		obj, err := s.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer obj.Body.Close()

		data, _ := io.ReadAll(obj.Body)
		if string(data) != "v2-new-content" {
			t.Errorf("Overwrite content = %q, want %q", string(data), "v2-new-content")
		}
		if obj.Size != 14 {
			t.Errorf("Overwrite size = %d, want 14", obj.Size)
		}
		if obj.ContentType != "application/octet-stream" {
			t.Errorf("Overwrite contentType = %q, want %q", obj.ContentType, "application/octet-stream")
		}
	})

	t.Run("MissingKey", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()

		_, err := s.Get(ctx, "does/not/exist.txt")
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Get missing key err = %v, want ErrNotFound", err)
		}

		_, err = s.Stat(ctx, "does/not/exist.txt")
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Stat missing key err = %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteMissingIsNil", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()

		if err := s.Delete(ctx, "never/created.txt"); err != nil {
			t.Errorf("Delete missing key returned error: %v", err)
		}
	})

	t.Run("InvalidKeys", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()

		invalidKeys := []string{
			"",
			"/leading/slash",
			"trailing/slash/",
			"double//slash",
			"has/./dot",
			"has/../dotdot",
			"back\\slash",
			"nul\x00byte",
			".",
			"..",
		}

		for _, k := range invalidKeys {
			if err := s.Put(ctx, k, strings.NewReader("x"), 1, "text/plain"); err == nil {
				t.Errorf("Put invalid key %q should fail", k)
			}
			if _, err := s.Get(ctx, k); err == nil {
				t.Errorf("Get invalid key %q should fail", k)
			}
			if _, err := s.Stat(ctx, k); err == nil {
				t.Errorf("Stat invalid key %q should fail", k)
			}
			if err := s.Delete(ctx, k); err == nil {
				t.Errorf("Delete invalid key %q should fail", k)
			}
		}
	})

	t.Run("DeletePrefixValidation", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()

		// Empty prefix must be rejected
		if err := s.DeletePrefix(ctx, ""); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("DeletePrefix(\"\") = %v, want ErrInvalidKey", err)
		}

		invalidPrefixes := []string{
			"/leading/slash",
			"double//slash",
			"has/./dot",
			"has/../dotdot",
			"back\\slash",
			"nul\x00byte",
		}
		for _, p := range invalidPrefixes {
			if err := s.DeletePrefix(ctx, p); !errors.Is(err, storage.ErrInvalidKey) {
				t.Errorf("DeletePrefix(%q) = %v, want ErrInvalidKey", p, err)
			}
		}
	})

	t.Run("DeletePrefix", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()

		// Write a keeper file under a different prefix
		keeperKey := "keep/important.txt"
		if err := s.Put(ctx, keeperKey, strings.NewReader("keep me"), 7, "text/plain"); err != nil {
			t.Fatalf("Put keeper failed: %v", err)
		}

		// Write >1000 objects to test S3 batching (1050 objects)
		const count = 1050
		prefix := "bulk-delete/"
		var eg errgroup.Group
		eg.SetLimit(50)

		for i := range count {
			idx := i
			eg.Go(func() error {
				k := fmt.Sprintf("%sobj-%04d", prefix, idx)
				return s.Put(ctx, k, strings.NewReader("1"), 1, "text/plain")
			})
		}
		if err := eg.Wait(); err != nil {
			t.Fatalf("Bulk puts failed: %v", err)
		}

		// Delete the prefix
		if err := s.DeletePrefix(ctx, prefix); err != nil {
			t.Fatalf("DeletePrefix failed: %v", err)
		}

		// Verify keeper still exists
		obj, err := s.Get(ctx, keeperKey)
		if err != nil {
			t.Fatalf("Keeper object was deleted: %v", err)
		}
		obj.Body.Close()

		// Verify first and last deleted objects return ErrNotFound
		for _, idx := range []int{0, 500, count - 1} {
			k := fmt.Sprintf("%sobj-%04d", prefix, idx)
			_, err := s.Stat(ctx, k)
			if !errors.Is(err, storage.ErrNotFound) {
				t.Errorf("Object %q still exists or wrong error: %v", k, err)
			}
		}
	})

	t.Run("PrefixIsolation", func(t *testing.T) {
		s1 := newStore(t)
		s2 := newStore(t)
		ctx := context.Background()

		key := "shared-path/file.txt"
		if err := s1.Put(ctx, key, strings.NewReader("store 1"), 7, "text/plain"); err != nil {
			t.Fatalf("s1 Put failed: %v", err)
		}

		// s2 should not see it
		_, err := s2.Get(ctx, key)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("s2 should not see s1's file: %v", err)
		}

		// s2 writes its own
		if err := s2.Put(ctx, key, strings.NewReader("store 2"), 7, "text/plain"); err != nil {
			t.Fatalf("s2 Put failed: %v", err)
		}

		// Check s1 didn't change
		obj1, err := s1.Get(ctx, key)
		if err != nil {
			t.Fatalf("s1 Get failed: %v", err)
		}
		b1, _ := io.ReadAll(obj1.Body)
		obj1.Body.Close()
		if string(b1) != "store 1" {
			t.Errorf("s1 content overwritten by s2: got %q", string(b1))
		}

		// s1 delete prefix should not affect s2
		if err := s1.DeletePrefix(ctx, "shared-path/"); err != nil {
			t.Fatalf("s1 DeletePrefix failed: %v", err)
		}
		obj2, err := s2.Get(ctx, key)
		if err != nil {
			t.Fatalf("s2 file deleted by s1 DeletePrefix: %v", err)
		}
		obj2.Body.Close()
	})

	t.Run("ConcurrentPuts", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()

		const concurrency = 20
		const putsPerWorker = 10
		var eg errgroup.Group
		eg.SetLimit(concurrency)

		for i := range concurrency {
			worker := i
			eg.Go(func() error {
				for j := range putsPerWorker {
					k := fmt.Sprintf("concurrent/%d-%d.txt", worker, j)
					val := fmt.Sprintf("val-%d-%d", worker, j)
					if err := s.Put(ctx, k, strings.NewReader(val), int64(len(val)), "text/plain"); err != nil {
						return err
					}
					info, err := s.Stat(ctx, k)
					if err != nil {
						return err
					}
					if info.Size != int64(len(val)) {
						return fmt.Errorf("size mismatch: got %d, want %d", info.Size, len(val))
					}
				}
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			t.Fatalf("Concurrent puts failed: %v", err)
		}
	})
}
