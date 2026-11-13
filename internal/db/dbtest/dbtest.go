// Package dbtest gives tests a migrated PostgreSQL database of their own.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ferrule-io/handbill/internal/db"
)

// Pool returns a pool on a fresh database created through TEST_DATABASE_URL
// (a role allowed to CREATE DATABASE, e.g. the Compose instance) and migrated
// by db.Open. The database is dropped at test cleanup, so tests and packages
// running in parallel never share one. Pool skips the test when
// TEST_DATABASE_URL is unset, and fails instead when CI is set.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("dbtest: TEST_DATABASE_URL must be set in CI")
		}
		t.Skip("dbtest: TEST_DATABASE_URL is unset (e.g. postgres://handbill:handbill@localhost:55432/handbill?sslmode=disable after `make db-up`)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("dbtest: connect to TEST_DATABASE_URL: %v", err)
	}
	defer admin.Close(ctx)

	var b [8]byte
	_, _ = rand.Read(b[:])
	name := "handbill_test_" + hex.EncodeToString(b[:])
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		t.Fatalf("dbtest: create database: %v", err)
	}
	t.Cleanup(func() { dropDatabase(t, url, ident) })

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("dbtest: parse TEST_DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.Database = name
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("dbtest: open %s: %v", name, err)
	}
	// Cleanups run last-in first-out: the pool closes before the drop.
	t.Cleanup(pool.Close)
	return pool
}

func dropDatabase(t *testing.T, url, ident string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Errorf("dbtest: connect to drop %s: %v", ident, err)
		return
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
		t.Errorf("dbtest: drop %s: %v", ident, err)
	}
}
