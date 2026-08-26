//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/mindfire-test/d-cron/internal/store"
)

// TestMigrate_TenReplicasConcurrent is the #34 acceptance run: 10 replicas
// racing Migrate on an empty database must all succeed (IF NOT EXISTS DDL
// under the dedicated migration advisory lock) and converge to exactly one
// schema/table/index set.
func TestMigrate_TenReplicasConcurrent(t *testing.T) {
	ctx := context.Background()
	db := mustOpen(t)

	const schema = "dcron_it_migrate"
	_, _ = db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)

	const replicas = 10
	errs := make([]error, replicas)
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = store.Migrate(ctx, db, schema)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d migrate: %v", i, err)
		}
	}

	var tables, indexes int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = $1 AND table_name = 'execution'`,
		schema).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_indexes
		 WHERE schemaname = $1 AND indexname = 'execution_job_time_idx'`,
		schema).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if tables != 1 || indexes != 1 {
		t.Fatalf("converged to tables=%d indexes=%d; want exactly one of each", tables, indexes)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})
}
