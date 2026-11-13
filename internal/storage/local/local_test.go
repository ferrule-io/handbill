package local_test

import (
	"path/filepath"
	"testing"

	"github.com/ferrule-io/handbill/internal/storage"
	"github.com/ferrule-io/handbill/internal/storage/local"
	"github.com/ferrule-io/handbill/internal/storage/storagetest"
	"github.com/google/uuid"
)

func TestLocal(t *testing.T) {
	tempBase := t.TempDir()
	storagetest.Run(t, func(t *testing.T) storage.Store {
		subDir := filepath.Join(tempBase, uuid.NewString())
		s, err := local.New(subDir)
		if err != nil {
			t.Fatalf("local.New: %v", err)
		}
		t.Cleanup(func() {
			_ = s.Close()
		})
		return s
	})
}
