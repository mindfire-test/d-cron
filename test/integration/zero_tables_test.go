//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	dcron "github.com/mindfire-test/d-cron/dcron"
)

// TestZeroTablesAfterLifecycle asserts that a full
// lifecycle with history disabled leaves zero d-cron tables in any
// user schema — the library must not create unexpected tables in the operator's database.
func TestZeroTablesAfterLifecycle(t *testing.T) {
	ctx := context.Background()
	db := mustOpen(t)

	s, err := dcron.New(
		db,
		dcron.WithNamespace("it-ac09"),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Add("noop", "@every 5ms", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)
	dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Stop(dctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema NOT IN ('pg_catalog','information_schema','dcron_it_migrate','dcron')
		   AND (table_name LIKE 'dcron%' OR table_name = 'execution')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("found %d d-cron tables after lifecycle; want 0", n)
	}
}
