package gcsstore_test

import (
	"context"
	"os"
	"testing"

	gcs "cloud.google.com/go/storage"
	"github.com/google/uuid"
	"google.golang.org/api/option"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/storage"
	"github.com/ferrule-io/handbill/internal/storage/gcsstore"
	"github.com/ferrule-io/handbill/internal/storage/storagetest"
)

func TestGCS(t *testing.T) {
	emulatorHost := os.Getenv("TEST_GCS_EMULATOR_HOST")
	if emulatorHost == "" {
		emulatorHost = os.Getenv("STORAGE_EMULATOR_HOST")
	}
	if emulatorHost == "" {
		t.Skip("TEST_GCS_EMULATOR_HOST not set; skipping GCS tests")
	}

	_ = os.Setenv("STORAGE_EMULATOR_HOST", emulatorHost)

	ctx := context.Background()
	bucket := "handbill-test-bucket"

	// Create bucket in fake-gcs-server if not exists
	client, err := gcs.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("gcs client: %v", err)
	}
	defer client.Close()

	_ = client.Bucket(bucket).Create(ctx, "test-project", nil)

	storagetest.Run(t, func(t *testing.T) storage.Store {
		prefix := "test-" + uuid.NewString()
		s, err := gcsstore.New(ctx, config.GCS{
			Bucket: bucket,
			Prefix: prefix,
		})
		if err != nil {
			t.Fatalf("gcsstore.New: %v", err)
		}
		t.Cleanup(func() {
			_ = s.Close()
		})
		return s
	})
}
