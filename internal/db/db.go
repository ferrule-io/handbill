// Package db owns the PostgreSQL connection pool, the embedded goose
// migrations, and the sqlc-generated queries (workstream W2a).
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates the connection pool. The pool connects lazily, so the server
// starts (and /healthz answers) before the database is reachable; /readyz
// reports the database state.
func Open(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	return pool, nil
}
