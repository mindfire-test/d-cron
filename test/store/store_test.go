package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mindfire-test/d-cron/internal/elector"
	"github.com/mindfire-test/d-cron/internal/store"
)

func TestNewValidatesSchemaAllowlist(t *testing.T) {
	t.Parallel()
	// Valid identifiers are accepted.
	for _, ok := range []string{"dcron", "d_cron2", "_x", strings.Repeat("a", 63)} {
		if _, err := store.New(nil, ok); err != nil {
			t.Errorf("store.New(nil, %q): %v; want accepted", ok, err)
		}
	}
	// Everything that could enable injection or break qualified names must be
	// refused rather than interpolated (NFR-503, issue #34).
	for _, bad := range []string{
		"public", // reserved: never litter the user's default schema
		"Dcron",  // uppercase identifier
		`"dcron"`, "dcron; DROP TABLE x", "dcron--", "1abc",
		strings.Repeat("a", 64), "a b",
	} {
		if _, err := store.New(nil, bad); !errors.Is(err, store.ErrInvalidSchema) {
			t.Errorf("store.New(nil, %q): err = %v; want store.ErrInvalidSchema", bad, err)
		}
	}
}

func TestMigrateRejectsInvalidSchemaWithoutDB(t *testing.T) {
	t.Parallel()
	// Validation happens before any database access, so an invalid schema is
	// rejected even with a nil handle.
	if err := store.Migrate(context.Background(), nil, `bad"; DROP SCHEMA x`); !errors.Is(err, store.ErrInvalidSchema) {
		t.Fatalf("Migrate with invalid schema err = %v; want store.ErrInvalidSchema", err)
	}
}

func TestMigrationDDLIsIdempotentAndQualified(t *testing.T) {
	t.Parallel()
	stmts := store.MigrationDDL("dcron")
	if len(stmts) != 3 {
		t.Fatalf("len(store.MigrationDDL) = %d; want 3 (schema, table, index)", len(stmts))
	}
	for i, want := range []string{
		"CREATE SCHEMA IF NOT EXISTS dcron",
		"CREATE TABLE IF NOT EXISTS dcron.execution",
		"CREATE INDEX IF NOT EXISTS dcron.execution_job_time_idx",
	} {
		if !strings.Contains(stmts[i], want) {
			t.Errorf("stmt[%d] = %q; want it to contain %q", i, stmts[i], want)
		}
	}
	// Every statement is IF NOT EXISTS so N concurrent replicas converge (#34).
	for i, stmt := range stmts {
		if !strings.Contains(stmt, "IF NOT EXISTS") {
			t.Errorf("stmt[%d] lacks IF NOT EXISTS: %q", i, stmt)
		}
	}
	// The execution table carries every status-bearing column from SDS §10.
	table := stmts[1]
	for _, col := range []string{
		"id", "namespace", "job_name", "scheduled_at", "started_at",
		"finished_at", "status", "attempt", "duration_ms", "error",
		"instance_id", "leader_epoch",
	} {
		if !strings.Contains(table, col) {
			t.Errorf("execution table lacks column %q:\n%s", col, table)
		}
	}
}

func TestSchemaLockKeyDistinctFromLeadershipKey(t *testing.T) {
	t.Parallel()
	got := store.SchemaLockKey("dcron")
	if got == 0 {
		t.Fatal("store.SchemaLockKey must never be zero")
	}
	if again := store.SchemaLockKey("dcron"); again != got {
		t.Fatal("store.SchemaLockKey not deterministic")
	}
	// The migration lock domain is disjoint from the leadership lock domain so
	// acquiring one never blocks the other (issue #34/FR-504).
	if got == elector.LockKey("default") || got == elector.LockKey("dcron") {
		t.Fatalf("migration key %d collides with a leadership key", got)
	}
	if other := store.SchemaLockKey("other"); other == got {
		t.Fatal("different schemas must yield different migration keys")
	}
}
