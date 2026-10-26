//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/internal/store"
)

func TestE2EAdminMutations_SurviveFailover(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	db := mustOpen(t)
	const namespace = "e2e-admin-failover"

	var r1SteadyFires, r2SteadyFires atomic.Int64
	var r1DynFires, r2DynFires atomic.Int64

	// Replica 1 setup
	s1, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithHistory(time.Hour),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(30*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}

	if err := s1.Add("steady-job", "@every 50ms", func(_ context.Context) error {
		r1SteadyFires.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("s1.Add: %v", err)
	}

	// Replica 2 setup (standby with registered handler for dynamic job)
	s2, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithHistory(time.Hour),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(30*time.Millisecond),
		dcron.WithJobHandler("dyn-job", func(_ context.Context) error {
			r2DynFires.Add(1)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}

	if err := s2.Add("steady-job", "@every 50ms", func(_ context.Context) error {
		r2SteadyFires.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("s2.Add: %v", err)
	}

	// Start s1 -> becomes Leader
	if err := s1.Start(ctx); err != nil {
		t.Fatalf("s1.Start: %v", err)
	}

	// Wait for s1 to become leader
	deadline := time.Now().Add(5 * time.Second)
	for s1.Leadership() != dcron.LeadershipLeader {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for s1 to become leader")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Start s2 -> enters Standby
	if err := s2.Start(ctx); err != nil {
		t.Fatalf("s2.Start: %v", err)
	}

	// Let steady-job fire on s1
	time.Sleep(120 * time.Millisecond)
	if r1SteadyFires.Load() == 0 {
		t.Fatal("s1: steady-job should have fired at least once")
	}

	// 1. Pause steady-job on active leader s1
	if err := s1.Pause("steady-job"); err != nil {
		t.Fatalf("s1.Pause: %v", err)
	}
	s1PausedFires := r1SteadyFires.Load()
	time.Sleep(120 * time.Millisecond)
	if r1SteadyFires.Load() != s1PausedFires {
		t.Errorf("s1: steady-job fired after being paused (got %d, want %d)", r1SteadyFires.Load(), s1PausedFires)
	}

	// 2. AddDynamic job on s1
	if err := s1.AddDynamic("dyn-job", "@every 50ms", func(_ context.Context) error {
		r1DynFires.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("s1.AddDynamic: %v", err)
	}

	// Verify duplicate AddDynamic is rejected with ErrJobExists
	if err := s1.AddDynamic("dyn-job", "@every 50ms", func(_ context.Context) error { return nil }); !errors.Is(err, dcron.ErrJobExists) {
		t.Fatalf("s1: duplicate AddDynamic should return ErrJobExists, got %v", err)
	}

	// Let dyn-job fire on s1
	time.Sleep(120 * time.Millisecond)
	if r1DynFires.Load() == 0 {
		t.Fatal("s1: dyn-job should have fired on leader")
	}

	// 3. Trigger failover: stop s1, allowing s2 to be promoted to Leader
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := s1.Stop(stopCtx); err != nil {
		t.Fatalf("s1.Stop: %v", err)
	}

	// Wait for s2 to be promoted
	deadline = time.Now().Add(5 * time.Second)
	for s2.Leadership() != dcron.LeadershipLeader {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for s2 promotion to leader")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Verify failover persistence on s2:
	// A) steady-job was paused on s1 -> MUST REMAIN PAUSED on s2!
	time.Sleep(150 * time.Millisecond)
	if r2SteadyFires.Load() != 0 {
		t.Errorf("s2: steady-job fired on promoted leader despite being paused before failover (fires=%d)", r2SteadyFires.Load())
	}

	// B) dyn-job was added on s1 -> MUST BE RESTORED and firing on s2!
	time.Sleep(150 * time.Millisecond)
	if r2DynFires.Load() == 0 {
		t.Errorf("s2: dyn-job should have been restored and firing after failover (fires=%d)", r2DynFires.Load())
	}

	// 4. Resume steady-job on newly promoted leader s2
	if err := s2.Resume("steady-job"); err != nil {
		t.Fatalf("s2.Resume: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if r2SteadyFires.Load() == 0 {
		t.Error("s2: steady-job should have fired after resume")
	}

	// 5. Remove steady-job on s2
	if err := s2.Remove("steady-job"); err != nil {
		t.Fatalf("s2.Remove: %v", err)
	}

	// Stop s2
	if err := s2.Stop(stopCtx); err != nil {
		t.Fatalf("s2.Stop: %v", err)
	}

	// 6. Start Replica 3 (new instance): steady-job must be reconciled as removed
	s3, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithHistory(time.Hour),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(30*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New s3: %v", err)
	}
	if err := s3.Add("steady-job", "@every 50ms", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("s3.Add: %v", err)
	}
	if err := s3.Start(ctx); err != nil {
		t.Fatalf("s3.Start: %v", err)
	}
	defer func() { _ = s3.Stop(stopCtx) }()

	deadline = time.Now().Add(5 * time.Second)
	for s3.Leadership() != dcron.LeadershipLeader {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for s3 promotion")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// On s3, steady-job was reconciled as removed from previous leader s2
	time.Sleep(100 * time.Millisecond)
	jobs := s3.Jobs()
	for _, j := range jobs {
		if j.Name == "steady-job" {
			t.Error("s3: steady-job should have been removed after failover reconciliation")
		}
	}
}

func TestE2EPrune_OrphanedRunningAndBatching(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx := context.Background()
	db := mustOpen(t)
	const schema = "dcron_it_prune"
	const namespace = "ns-prune-test"

	_, _ = db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	}()

	if err := store.Migrate(ctx, db, schema); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	st, err := store.New(db, schema)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	// Insert epoch
	if _, err := st.IncrementEpoch(ctx, namespace, "node-1"); err != nil {
		t.Fatalf("IncrementEpoch: %v", err)
	}

	// 1. Insert 3 finished rows older than 2 hours
	for i := 1; i <= 3; i++ {
		rowID, err := st.Record(ctx, store.Execution{
			Namespace:   namespace,
			JobName:     "old-job",
			ScheduledAt: time.Now().Add(-3 * time.Hour),
			StartedAt:   time.Now().Add(-3 * time.Hour),
			Status:      store.StatusRunning,
			Attempt:     1,
			InstanceID:  "node-1",
			LeaderEpoch: 1,
		})
		if err != nil {
			t.Fatalf("Record old: %v", err)
		}
		if _, err := st.Finish(ctx, rowID, store.Execution{
			Namespace:  namespace,
			Status:     store.StatusSuccess,
			FinishedAt: time.Now().Add(-2 * time.Hour),
			DurationMs: 100,
		}); err != nil {
			t.Fatalf("Finish old: %v", err)
		}
	}

	// 2. Insert 2 orphaned running rows older than 2 hours (simulating crashed node or fenced finish)
	for i := 1; i <= 2; i++ {
		_, err := st.Record(ctx, store.Execution{
			Namespace:   namespace,
			JobName:     "crashed-job",
			ScheduledAt: time.Now().Add(-3 * time.Hour),
			StartedAt:   time.Now().Add(-3 * time.Hour),
			Status:      store.StatusRunning,
			Attempt:     1,
			InstanceID:  "node-crashed",
			LeaderEpoch: 1,
		})
		if err != nil {
			t.Fatalf("Record crashed: %v", err)
		}
	}

	// 3. Insert 1 recent row (10 minutes ago)
	recentID, err := st.Record(ctx, store.Execution{
		Namespace:   namespace,
		JobName:     "recent-job",
		ScheduledAt: time.Now().Add(-10 * time.Minute),
		StartedAt:   time.Now().Add(-10 * time.Minute),
		Status:      store.StatusRunning,
		Attempt:     1,
		InstanceID:  "node-1",
		LeaderEpoch: 1,
	})
	if err != nil {
		t.Fatalf("Record recent: %v", err)
	}
	if _, err := st.Finish(ctx, recentID, store.Execution{
		Namespace:  namespace,
		Status:     store.StatusSuccess,
		FinishedAt: time.Now().Add(-9 * time.Minute),
		DurationMs: 100,
	}); err != nil {
		t.Fatalf("Finish recent: %v", err)
	}

	// Verify EXPLAIN query plan for the prune selection
	explainQ := `EXPLAIN (FORMAT TEXT) SELECT id FROM (
		SELECT id FROM ` + schema + `.execution
		WHERE namespace = '` + namespace + `'
		  AND status <> 'running'
		  AND finished_at < now() - interval '1 hour'
		UNION ALL
		SELECT id FROM ` + schema + `.execution
		WHERE namespace = '` + namespace + `'
		  AND status = 'running'
		  AND started_at < now() - interval '1 hour'
	) AS candidates ORDER BY id ASC LIMIT 10`
	planRows, err := db.QueryContext(ctx, explainQ)
	if err != nil {
		t.Fatalf("EXPLAIN prune query: %v", err)
	}
	var planLines []string
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		planLines = append(planLines, line)
	}
	_ = planRows.Close()
	planOutput := strings.Join(planLines, "\n")
	t.Logf("Prune query plan:\n%s", planOutput)
	if len(planLines) == 0 {
		t.Fatal("empty EXPLAIN plan for prune query")
	}

	// 4. Test multiple namespaces isolation: row in other-namespace is NOT deleted
	if _, err := st.IncrementEpoch(ctx, "other-namespace", "node-other"); err != nil {
		t.Fatalf("IncrementEpoch other namespace: %v", err)
	}
	otherRowID, err := st.Record(ctx, store.Execution{
		Namespace:   "other-namespace",
		JobName:     "other-job",
		ScheduledAt: time.Now().Add(-3 * time.Hour),
		StartedAt:   time.Now().Add(-3 * time.Hour),
		Status:      store.StatusRunning,
		Attempt:     1,
		InstanceID:  "node-other",
		LeaderEpoch: 1,
	})
	if err != nil {
		t.Fatalf("Record other namespace: %v", err)
	}
	if _, err := st.Finish(ctx, otherRowID, store.Execution{
		Namespace:  "other-namespace",
		Status:     store.StatusSuccess,
		FinishedAt: time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("Finish other: %v", err)
	}

	// Prune with retention of 1 hour, using batch size of 2 to exercise batch deletion loop
	deleted, err := st.PruneBatch(ctx, namespace, 1*time.Hour, 2)
	if err != nil {
		t.Fatalf("PruneBatch: %v", err)
	}

	// 3 old finished + 2 old orphaned running = 5 deleted rows for 'namespace'
	if deleted != 5 {
		t.Fatalf("PruneBatch deleted %d rows; want exactly 5 (3 finished + 2 orphaned running)", deleted)
	}

	// Repeated prune immediately returns 0 rows deleted
	repeatDeleted, err := st.PruneBatch(ctx, namespace, 1*time.Hour, 2)
	if err != nil {
		t.Fatalf("PruneBatch repeat: %v", err)
	}
	if repeatDeleted != 0 {
		t.Fatalf("repeat PruneBatch deleted %d rows; want 0", repeatDeleted)
	}

	// Verify only 1 recent row remains for 'namespace'
	remaining, err := st.Recent(ctx, namespace, "", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("remaining rows = %d; want 1 recent execution", len(remaining))
	}
	if remaining[0].JobName != "recent-job" {
		t.Errorf("remaining job = %q; want recent-job", remaining[0].JobName)
	}

	// Verify row in other-namespace was NOT deleted
	otherRemaining, err := st.Recent(ctx, "other-namespace", "", 10)
	if err != nil {
		t.Fatalf("Recent other namespace: %v", err)
	}
	if len(otherRemaining) != 1 {
		t.Fatalf("other-namespace rows = %d; want 1 (not deleted)", len(otherRemaining))
	}
}

func TestE2EReservedKeywordSchema(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx := context.Background()
	db := mustOpen(t)

	// "order" and "user" are strict PostgreSQL keywords that fail without double quoting
	for _, schema := range []string{"order", "user"} {
		_, _ = db.ExecContext(ctx, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
		defer func(s string) {
			_, _ = db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+s+`" CASCADE`)
		}(schema)

		if err := store.Migrate(ctx, db, schema); err != nil {
			t.Fatalf("Migrate schema %q: %v", schema, err)
		}

		st, err := store.New(db, schema)
		if err != nil {
			t.Fatalf("store.New(%q): %v", schema, err)
		}

		epoch, err := st.IncrementEpoch(ctx, "ns", "node-1")
		if err != nil {
			t.Fatalf("IncrementEpoch on schema %q: %v", schema, err)
		}

		rowID, err := st.Record(ctx, store.Execution{
			Namespace:   "ns",
			JobName:     "keyword-job",
			ScheduledAt: time.Now(),
			StartedAt:   time.Now(),
			Status:      store.StatusRunning,
			Attempt:     1,
			InstanceID:  "node-1",
			LeaderEpoch: epoch,
		})
		if err != nil {
			t.Fatalf("Record on schema %q: %v", schema, err)
		}

		if _, err := st.Finish(ctx, rowID, store.Execution{
			Namespace:  "ns",
			Status:     store.StatusSuccess,
			FinishedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Finish on schema %q: %v", schema, err)
		}

		if err := st.SaveJobState(ctx, store.JobState{
			Namespace: "ns",
			JobName:   "keyword-job",
			Paused:    true,
		}); err != nil {
			t.Fatalf("SaveJobState on schema %q: %v", schema, err)
		}

		states, err := st.GetJobStates(ctx, "ns")
		if err != nil {
			t.Fatalf("GetJobStates on schema %q: %v", schema, err)
		}
		if !states["keyword-job"].Paused {
			t.Fatalf("expected keyword-job to be paused in schema %q", schema)
		}
	}
}
