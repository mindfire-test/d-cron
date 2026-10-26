package store_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/mindfire-test/d-cron/internal/elector"
	"github.com/mindfire-test/d-cron/internal/store"
)

type storeFakeDriver struct{}

func (storeFakeDriver) Open(string) (driver.Conn, error) { return storeFakeConn{}, nil }

type storeFakeConn struct{}

func (storeFakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("fake: no stmt") }
func (storeFakeConn) Close() error                        { return nil }
func (storeFakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("fake: no tx") }

func init() {
	sql.Register("dcron-store-fake", storeFakeDriver{})
}

func TestNewValidatesSchemaAllowlist(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"dcron", "d_cron2", "_x", strings.Repeat("a", 63)} {
		if _, err := store.New(nil, ok); err != nil {
			t.Errorf("store.New(nil, %q): %v; want accepted", ok, err)
		}
	}

	for _, bad := range []string{
		"public",
		"Dcron",
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

	if err := store.Migrate(context.Background(), nil, `bad"; DROP SCHEMA x`); !errors.Is(err, store.ErrInvalidSchema) {
		t.Fatalf("Migrate with invalid schema err = %v; want store.ErrInvalidSchema", err)
	}
}

func TestMigrationDDLIsIdempotentAndQualified(t *testing.T) {
	t.Parallel()
	stmts := store.MigrationDDL("dcron")
	if len(stmts) != 7 {
		t.Fatalf("len(store.MigrationDDL) = %d; want 7 (schema, epoch table, execution table, job_state table, 3 indexes)", len(stmts))
	}
	for i, want := range []string{
		`CREATE SCHEMA IF NOT EXISTS "dcron"`,
		`CREATE TABLE IF NOT EXISTS "dcron"."leader_epoch"`,
		`CREATE TABLE IF NOT EXISTS "dcron"."execution"`,
		`CREATE TABLE IF NOT EXISTS "dcron"."job_state"`,
		`CREATE INDEX IF NOT EXISTS execution_job_time_idx`,
		`CREATE INDEX IF NOT EXISTS execution_prune_idx`,
		`CREATE INDEX IF NOT EXISTS execution_prune_running_idx`,
	} {
		if !strings.Contains(stmts[i], want) {
			t.Errorf("stmt[%d] = %q; want it to contain %q", i, stmts[i], want)
		}
	}

	for i, stmt := range stmts {
		if !strings.Contains(stmt, "IF NOT EXISTS") {
			t.Errorf("stmt[%d] lacks IF NOT EXISTS: %q", i, stmt)
		}
	}

	table := stmts[2]
	for _, col := range []string{
		"id", "namespace", "job_name", "scheduled_at", "started_at",
		"finished_at", "status", "attempt", "duration_ms", "error",
		"instance_id", "leader_epoch",
	} {
		if !strings.Contains(table, col) {
			t.Errorf("execution table lacks column %q:\n%s", col, table)
		}
	}

	jobStateTable := stmts[3]
	for _, col := range []string{
		"namespace", "job_name", "paused", "removed", "spec", "updated_at",
	} {
		if !strings.Contains(jobStateTable, col) {
			t.Errorf("job_state table lacks column %q:\n%s", col, jobStateTable)
		}
	}

	if !strings.Contains(stmts[4], `ON "dcron"."execution" (namespace, job_name, scheduled_at DESC)`) {
		t.Errorf("stmt[4] lacks expected target and columns: %q", stmts[4])
	}
	if !strings.Contains(stmts[5], `ON "dcron"."execution" (namespace, finished_at)`) {
		t.Errorf("stmt[5] lacks expected target and columns: %q", stmts[5])
	}
}

func TestMigrationDDLSchemaQualification(t *testing.T) {
	t.Parallel()
	const custom = "custom_schema"
	stmts := store.MigrationDDL(custom)
	if len(stmts) != 7 {
		t.Fatalf("len(store.MigrationDDL) = %d; want 7", len(stmts))
	}

	for i, want := range []string{
		`CREATE SCHEMA IF NOT EXISTS "` + custom + `"`,
		`CREATE TABLE IF NOT EXISTS "` + custom + `"."leader_epoch"`,
		`CREATE TABLE IF NOT EXISTS "` + custom + `"."execution"`,
		`CREATE TABLE IF NOT EXISTS "` + custom + `"."job_state"`,
		`ON "` + custom + `"."execution" (namespace, job_name, scheduled_at DESC)`,
		`ON "` + custom + `"."execution" (namespace, finished_at)`,
		`ON "` + custom + `"."execution" (namespace, started_at) WHERE status = 'running'`,
	} {
		if !strings.Contains(stmts[i], want) {
			t.Errorf("stmt[%d] = %q; want it to contain %q", i, stmts[i], want)
		}
		if !strings.Contains(stmts[i], "IF NOT EXISTS") {
			t.Errorf("stmt[%d] lacks IF NOT EXISTS: %q", i, stmts[i])
		}
	}
}

func TestMigrationDDLReservedKeywords(t *testing.T) {
	t.Parallel()
	for _, keyword := range []string{"user", "order", "group", "select", "table"} {
		stmts := store.MigrationDDL(keyword)
		for _, stmt := range stmts {
			if !strings.Contains(stmt, `"`+keyword+`"`) {
				t.Errorf("expected quoted schema %q in stmt: %s", keyword, stmt)
			}
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

	if got == elector.LockKey("default") || got == elector.LockKey("dcron") {
		t.Fatalf("migration key %d collides with a leadership key", got)
	}
	if other := store.SchemaLockKey("other"); other == got {
		t.Fatal("different schemas must yield different migration keys")
	}
}

func TestStoreOperationsHonorContextCancellation(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("dcron-store-fake", "unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled immediately

	st, err := store.New(db, "dcron")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Migrate with canceled context
	if err := store.Migrate(ctx, db, "dcron"); !errors.Is(err, context.Canceled) {
		t.Errorf("Migrate with canceled ctx err = %v; want context.Canceled", err)
	}

	// 2. Record with canceled context
	if _, err := st.Record(ctx, store.Execution{Namespace: "ns", JobName: "j", LeaderEpoch: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("Record with canceled ctx err = %v; want context.Canceled", err)
	}

	// 3. Finish with canceled context
	if _, err := st.Finish(ctx, 1, store.Execution{Namespace: "ns"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Finish with canceled ctx err = %v; want context.Canceled", err)
	}

	// 4. Prune with canceled context
	if _, err := st.Prune(ctx, "ns", 100); !errors.Is(err, context.Canceled) {
		t.Errorf("Prune with canceled ctx err = %v; want context.Canceled", err)
	}

	// 5. IncrementEpoch with canceled context
	if _, err := st.IncrementEpoch(ctx, "ns", "inst-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("IncrementEpoch with canceled ctx err = %v; want context.Canceled", err)
	}

	// 6. SaveJobState with canceled context
	if err := st.SaveJobState(ctx, store.JobState{Namespace: "ns", JobName: "j"}); !errors.Is(err, context.Canceled) {
		t.Errorf("SaveJobState with canceled ctx err = %v; want context.Canceled", err)
	}

	// 7. GetJobState with canceled context
	if _, err := st.GetJobState(ctx, "ns", "j"); !errors.Is(err, context.Canceled) {
		t.Errorf("GetJobState with canceled ctx err = %v; want context.Canceled", err)
	}

	// 8. GetJobStates with canceled context
	if _, err := st.GetJobStates(ctx, "ns"); !errors.Is(err, context.Canceled) {
		t.Errorf("GetJobStates with canceled ctx err = %v; want context.Canceled", err)
	}
}
