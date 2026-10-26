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
