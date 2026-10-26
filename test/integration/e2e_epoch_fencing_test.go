//go:build integration

package integration

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/internal/store"
)

func TestE2EEpochPromotionAndFencing(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := mustOpen(t)
	namespace := "e2e-epoch-propagation"

	// Create scheduler 1 (Replica 1) with durable history enabled
	s1, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithHistory(time.Hour),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}

	var s1Epoch atomic.Int64
	var s1Fenced atomic.Bool
	s1Fired := make(chan struct{})

	err = s1.Add("e2e-job", "@every 500ms", func(jobCtx context.Context) error {
		ep := dcron.Epoch(jobCtx)
		s1Epoch.Store(ep)

		// Assert dcron.Fence succeeds inside a user transaction
		tx, err := db.BeginTx(jobCtx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		if err := dcron.Fence(jobCtx, tx); err != nil {
			s1Fenced.Store(true)
			return err
		}
		_ = tx.Commit()

		select {
		case <-s1Fired:
		default:
			close(s1Fired)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Add s1: %v", err)
	}

	if err := s1.Start(ctx); err != nil {
		t.Fatalf("Start s1: %v", err)
	}

	select {
	case <-s1Fired:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for s1 job fire")
	}

	// Stop s1 to release the advisory lock cleanly and allow failover to replica 2
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := s1.Stop(stopCtx); err != nil {
		t.Fatalf("Stop s1: %v", err)
	}

	epoch1 := s1Epoch.Load()
	if epoch1 < 1 {
		t.Fatalf("epoch1 = %d; want >= 1", epoch1)
	}
	if s1Fenced.Load() {
		t.Fatal("s1 job was unexpectedly fenced")
	}

	// Verify history was recorded in store with the correct leader epoch
	st, err := store.New(db, "dcron")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	recent1, err := st.Recent(ctx, namespace, "e2e-job", 10)
	if err != nil {
		t.Fatalf("Recent s1: %v", err)
	}
	var found1 bool
	for _, r := range recent1 {
		if r.LeaderEpoch == epoch1 && r.Status == store.StatusSuccess {
			found1 = true
			break
		}
	}
	if !found1 {
		t.Fatalf("expected successful execution row with LeaderEpoch %d, rows: %+v", epoch1, recent1)
	}

	// Create scheduler 2 (Replica 2) for the same namespace
	s2, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithHistory(time.Hour),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}

	var s2Epoch atomic.Int64
	var s2Fenced atomic.Bool
	s2Fired := make(chan struct{})

	err = s2.Add("e2e-job", "@every 500ms", func(jobCtx context.Context) error {
		ep := dcron.Epoch(jobCtx)
		s2Epoch.Store(ep)

		tx, err := db.BeginTx(jobCtx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		if err := dcron.Fence(jobCtx, tx); err != nil {
			s2Fenced.Store(true)
			return err
		}
		_ = tx.Commit()

		select {
		case <-s2Fired:
		default:
			close(s2Fired)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Add s2: %v", err)
	}

	if err := s2.Start(ctx); err != nil {
		t.Fatalf("Start s2: %v", err)
	}

	select {
	case <-s2Fired:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for s2 job fire")
	}

	// Stop s2 to ensure all in-flight jobs and history finish writes complete
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	if err := s2.Stop(dctx); err != nil {
		t.Fatalf("Stop s2: %v", err)
	}

	epoch2 := s2Epoch.Load()
	if epoch2 <= epoch1 {
		t.Fatalf("epoch2 = %d must be strictly greater than epoch1 = %d", epoch2, epoch1)
	}
	if s2Fenced.Load() {
		t.Fatal("s2 job was unexpectedly fenced")
	}

	recent2, err := st.Recent(ctx, namespace, "e2e-job", 10)
	if err != nil {
		t.Fatalf("Recent s2: %v", err)
	}
	var found2 bool
	for _, r := range recent2 {
		if r.LeaderEpoch == epoch2 && r.Status == store.StatusSuccess {
			found2 = true
			break
		}
	}
	if !found2 {
		t.Fatalf("expected successful execution row with LeaderEpoch %d, rows: %+v", epoch2, recent2)
	}

	// Verify that a stale epoch1 write is properly rejected by dcron.Fence
	txStale, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx stale: %v", err)
	}
	defer func() { _ = txStale.Rollback() }()

	ctxStale := dcron.WithNamespaceKey(dcron.WithEpoch(ctx, epoch1), namespace)
	if err := dcron.Fence(ctxStale, txStale); err == nil {
		t.Fatalf("dcron.Fence with stale epoch %d should be rejected by DB epoch %d", epoch1, epoch2)
	}
}

// TestScheduler_WithHistory_InspectPersistedRows verifies that enabling WithHistory on the
// scheduler causes executed jobs to persist detailed execution records in PostgreSQL, asserting
// job_name, namespace, status, attempt, duration, timestamps, and matching leader_epoch.
func TestScheduler_WithHistory_InspectPersistedRows(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := mustOpen(t)
	namespace := "inspect-persisted-rows"

	s, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithHistory(time.Hour),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	jobDone := make(chan struct{})
	jobStarted := time.Now()

	err = s.Add("history-inspect-job", "@every 10ms", func(ctx context.Context) error {
		time.Sleep(25 * time.Millisecond)
		select {
		case <-jobDone:
		default:
			close(jobDone)
		}
		return nil
	}, dcron.WithNoOverlap())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-jobDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job execution")
	}

	schedEpoch := s.Epoch()
	if schedEpoch < 1 {
		t.Fatalf("s.Epoch() = %d; want >= 1", schedEpoch)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	st, err := store.New(db, "dcron")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	recent, err := st.Recent(ctx, namespace, "history-inspect-job", 5)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) == 0 {
		t.Fatal("no execution rows persisted for history-inspect-job")
	}

	row := recent[0]
	if row.JobName != "history-inspect-job" {
		t.Errorf("row.JobName = %q; want %q", row.JobName, "history-inspect-job")
	}
	if row.Namespace != namespace {
		t.Errorf("row.Namespace = %q; want %q", row.Namespace, namespace)
	}
	if row.Status != store.StatusSuccess {
		t.Errorf("row.Status = %q; want %q", row.Status, store.StatusSuccess)
	}
	if row.Attempt != 1 {
		t.Errorf("row.Attempt = %d; want 1", row.Attempt)
	}
	if row.DurationMs < 20 {
		t.Errorf("row.DurationMs = %d; want >= 20", row.DurationMs)
	}
	if row.LeaderEpoch != schedEpoch {
		t.Errorf("row.LeaderEpoch = %d; want %d (matching scheduler epoch)", row.LeaderEpoch, schedEpoch)
	}
	if row.StartedAt.Before(jobStarted.Add(-5 * time.Second)) {
		t.Errorf("row.StartedAt is unreasonably old: %v", row.StartedAt)
	}
	if row.FinishedAt.Before(row.StartedAt) {
		t.Errorf("row.FinishedAt %v before StartedAt %v", row.FinishedAt, row.StartedAt)
	}
}

// TestScheduler_EpochFencing_StaleSchedulerFinishRejected simulates a long-running job whose leader
// was superseded while the job was in flight. It verifies that when the stale scheduler finishes,
// the store's leader epoch fencing rejects the update and logs/records a fenced write.
func TestScheduler_EpochFencing_StaleSchedulerFinishRejected(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := mustOpen(t)
	namespace := "stale-finish-fenced"

	st, err := store.New(db, "dcron")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	s1, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithHistory(time.Hour),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}

	jobStarted := make(chan struct{})
	unblockJob := make(chan struct{})
	jobFinished := make(chan struct{})

	err = s1.Add("stale-fenced-job", "@every 10ms", func(jobCtx context.Context) error {
		select {
		case <-jobStarted:
		default:
			close(jobStarted)
		}
		<-unblockJob
		close(jobFinished)
		return nil
	}, dcron.WithNoOverlap())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s1.Start(ctx); err != nil {
		t.Fatalf("Start s1: %v", err)
	}

	select {
	case <-jobStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job to start")
	}

	s1Epoch := s1.Epoch()
	if s1Epoch < 1 {
		t.Fatalf("s1.Epoch() = %d; want >= 1", s1Epoch)
	}

	// While s1's job is blocked mid-execution, advance current_epoch in the database
	// simulating failover or another replica being promoted to a higher epoch.
	newEpoch, err := st.IncrementEpoch(ctx, namespace, "replica-2-promoted")
	if err != nil {
		t.Fatalf("IncrementEpoch: %v", err)
	}
	if newEpoch <= s1Epoch {
		t.Fatalf("newEpoch = %d must be strictly greater than s1Epoch = %d", newEpoch, s1Epoch)
	}

	// Unblock s1's job. s1 completes execution and attempts s.store.Finish with stale epoch s1Epoch.
	close(unblockJob)

	select {
	case <-jobFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for s1 job to finish")
	}

	// Stop s1
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	_ = s1.Stop(stopCtx)

	// In the store, the execution row recorded by s1 at s1Epoch was initially status='running'.
	// Because newEpoch > s1Epoch, s.store.Finish WHERE leader_epoch >= current_epoch matched 0 rows!
	// Therefore the row was NOT updated to 'success'.
	recent, err := st.Recent(ctx, namespace, "stale-fenced-job", 5)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) == 0 {
		t.Fatal("no execution rows found")
	}

	row := recent[0]
	if row.Status == store.StatusSuccess {
		t.Fatalf("stale leader s1 at epoch %d unexpectedly succeeded in writing status=success against DB at epoch %d", s1Epoch, newEpoch)
	}
	if row.Status != store.StatusRunning {
		t.Fatalf("row.Status = %q; want running (fenced write was rejected)", row.Status)
	}
}
