// Package db owns the PostgreSQL connection pool, the embedded goose
// migrations (migrations/), and the sqlc-generated queries (queries/ →
// *.sql.go, models.go, dbtx.go).
//
// Schema conventions are documented at the top of the first migration.
package db

//go:generate sqlc generate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open creates the connection pool and applies every pending migration
// before returning, so it fails when the database is unreachable. Several
// replicas may start at once: a PostgreSQL advisory lock serializes them.
func Open(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	p, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer p.Close()
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("db: apply migrations: %w", err)
	}
	return nil
}

// newProvider returns a goose provider over the embedded migrations. Closing
// it does not close pool.
func newProvider(pool *pgxpool.Pool) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), fsys,
		goose.WithSessionLocker(locker),
		// Migrations are named by timestamp on parallel branches, so one may
		// land after a newer one has been applied.
		goose.WithAllowOutofOrder(true),
	)
	if err != nil {
		return nil, fmt.Errorf("db: migrations: %w", err)
	}
	return p, nil
}

// InTx runs fn in a transaction, committing when it returns nil and rolling
// back otherwise.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(q *Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	if err := fn(New(tx)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("db: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}
