package storage

import (
	"context"
	"fmt"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/storage/gcsstore"
	"github.com/ferrule-io/handbill/internal/storage/local"
	"github.com/ferrule-io/handbill/internal/storage/s3store"
)

// Open creates a Store matching cfg.Type.
func Open(ctx context.Context, cfg config.Storage) (Store, error) {
	switch cfg.Type {
	case config.StorageLocal:
		return local.New(cfg.LocalDir)
	case config.StorageS3:
		return s3store.New(ctx, cfg.S3)
	case config.StorageGCS:
		return gcsstore.New(ctx, cfg.GCS)
	default:
		return nil, fmt.Errorf("storage: unsupported type %q", cfg.Type)
	}
}
