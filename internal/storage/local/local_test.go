package local_test

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/mjquinlan2000/agent-dash/internal/storage"
	"github.com/mjquinlan2000/agent-dash/internal/storage/local"
	"github.com/mjquinlan2000/agent-dash/internal/storage/storagetest"
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
