package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/storage"
)

func TestValidKey(t *testing.T) {
	valid := []string{
		"file.txt",
		"a/b/c.txt",
		"sites/abc234/blobs/deadbeef0123456789abcdef",
		"dir/sub.dir/file-name_123.jpg",
	}
	for _, k := range valid {
		if err := storage.ValidKey(k); err != nil {
			t.Errorf("ValidKey(%q) = %v, want nil", k, err)
		}
	}

	invalid := []string{
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
	for _, k := range invalid {
		if err := storage.ValidKey(k); err == nil {
			t.Errorf("ValidKey(%q) = nil, want error", k)
		}
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()

	t.Run("Local", func(t *testing.T) {
		tempDir := t.TempDir()
		s, err := storage.Open(ctx, config.Storage{
			Type:     config.StorageLocal,
			LocalDir: tempDir,
		})
		if err != nil {
			t.Fatalf("Open local failed: %v", err)
		}
		if err := s.Ping(ctx); err != nil {
			t.Fatalf("Ping failed: %v", err)
		}
	})

	t.Run("S3", func(t *testing.T) {
		endpoint := os.Getenv("TEST_S3_ENDPOINT")
		if endpoint == "" {
			endpoint = os.Getenv("S3_ENDPOINT")
		}
		if endpoint == "" {
			t.Skip("TEST_S3_ENDPOINT not set; skipping S3 test")
		}
		if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
			_ = os.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
		}
		if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
			_ = os.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")
		}

		s, err := storage.Open(ctx, config.Storage{
			Type: config.StorageS3,
			S3: config.S3{
				Bucket:         "agent-dash-test",
				Region:         "us-east-1",
				Endpoint:       endpoint,
				Prefix:         "open-test",
				ForcePathStyle: true,
			},
		})
		if err != nil {
			t.Fatalf("Open S3 failed: %v", err)
		}
		if err := s.Ping(ctx); err != nil {
			t.Fatalf("Ping failed: %v", err)
		}
	})

	t.Run("GCS", func(t *testing.T) {
		emulatorHost := os.Getenv("TEST_GCS_EMULATOR_HOST")
		if emulatorHost == "" {
			emulatorHost = os.Getenv("STORAGE_EMULATOR_HOST")
		}
		if emulatorHost == "" {
			t.Skip("TEST_GCS_EMULATOR_HOST not set; skipping GCS test")
		}
		_ = os.Setenv("STORAGE_EMULATOR_HOST", emulatorHost)

		s, err := storage.Open(ctx, config.Storage{
			Type: config.StorageGCS,
			GCS: config.GCS{
				Bucket: "agent-dash-test-bucket",
				Prefix: "open-test",
			},
		})
		if err != nil {
			t.Fatalf("Open GCS failed: %v", err)
		}
		if err := s.Ping(ctx); err != nil {
			t.Fatalf("Ping failed: %v", err)
		}
	})

	t.Run("InvalidType", func(t *testing.T) {
		_, err := storage.Open(ctx, config.Storage{
			Type: config.StorageType("invalid"),
		})
		if err == nil {
			t.Fatalf("Open invalid type should fail")
		}
	})
}
