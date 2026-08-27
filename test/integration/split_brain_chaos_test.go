//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/internal/store"
)

func TestSplitBrainEpochFencing(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := mustOpen(t)

	s, err := store.New(db, "dcron")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := store.Migrate(ctx, db, "dcron"); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}

	namespace := "test-split-brain"
	epoch1, err := s.IncrementEpoch(ctx, namespace, "replica-1")
	if err != nil {
		t.Fatalf("IncrementEpoch 1: %v", err)
	}
	if epoch1 < 1 {
		t.Fatalf("epoch1 = %d; want >= 1", epoch1)
	}

	// Opening write for epoch 1
	rowID, err := s.Record(ctx, store.Execution{
		Namespace:   namespace,
		JobName:     "fenced-job",
		ScheduledAt: time.Now(),
		StartedAt:   time.Now(),
		Status:      store.StatusRunning,
		Attempt:     1,
		InstanceID:  "replica-1",
		LeaderEpoch: epoch1,
	})
	if err != nil {
		t.Fatalf("Record for epoch1: %v", err)
	}

	// Now promote replica-2 (simulating failover and epoch increment)
	epoch2, err := s.IncrementEpoch(ctx, namespace, "replica-2")
	if err != nil {
		t.Fatalf("IncrementEpoch 2: %v", err)
	}
	if epoch2 <= epoch1 {
		t.Fatalf("epoch2 (%d) must be strictly greater than epoch1 (%d)", epoch2, epoch1)
	}

	// Replica-1 (stale leader) attempts to finish its execution row with epoch1
	rowsAffected, err := s.Finish(ctx, rowID, store.Execution{
		Namespace:  namespace,
		Status:     store.StatusSuccess,
		FinishedAt: time.Now(),
		DurationMs: 100,
		Attempt:    1,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if rowsAffected != 0 {
		t.Fatalf("rowsAffected = %d; want 0 (fenced write rejected due to stale epoch)", rowsAffected)
	}

	// Verify User Fencing helper inside user transaction
	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx 1: %v", err)
	}
	ctxEpoch1 := dcron.WithNamespaceKey(dcron.WithEpoch(ctx, epoch1), namespace)
	if err := dcron.Fence(ctxEpoch1, tx1); err == nil {
		t.Fatal("dcron.Fence with stale epoch1 inside tx should have failed")
	}
	_ = tx1.Rollback()

	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx 2: %v", err)
	}
	ctxEpoch2 := dcron.WithNamespaceKey(dcron.WithEpoch(ctx, epoch2), namespace)
	if err := dcron.Fence(ctxEpoch2, tx2); err != nil {
		t.Fatalf("dcron.Fence with current epoch2 failed: %v", err)
	}
	_ = tx2.Rollback()
}
