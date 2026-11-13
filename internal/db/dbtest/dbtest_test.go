package dbtest_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ferrule-io/handbill/internal/db/dbtest"
)

func TestPool(t *testing.T) {
	var names [2]string
	t.Run("pools", func(t *testing.T) {
		for i := range names {
			pool := dbtest.Pool(t)
			if err := pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&names[i]); err != nil {
				t.Fatal(err)
			}
			var tables int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'sites'").Scan(&tables); err != nil || tables != 1 {
				t.Fatalf("database %s is not migrated: %d, %v", names[i], tables, err)
			}
		}
		if names[0] == names[1] {
			t.Fatalf("two pools share database %s", names[0])
		}
	})
	if t.Skipped() || names[0] == "" {
		return
	}

	// The subtest's cleanup dropped both databases.
	conn, err := pgx.Connect(t.Context(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())
	var n int
	if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM pg_database WHERE datname = ANY($1)", names[:]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d test databases left after cleanup", n)
	}
}
