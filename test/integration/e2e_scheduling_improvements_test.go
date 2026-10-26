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

func TestE2ESinceLastSuccessScheduling(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := mustOpen(t)
	namespace := "e2e-since-success"

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

	var executions atomic.Int64
	var lastFinished atomic.Pointer[time.Time]
	secondFireIntervalCh := make(chan time.Duration, 1)

	interval := 150 * time.Millisecond
	workDuration := 80 * time.Millisecond

	err = s.Add("since-job", "@every 1m", func(_ context.Context) error {
		count := executions.Add(1)
		time.Sleep(workDuration)
		now := time.Now()
		if count == 1 {
			lastFinished.Store(&now)
		} else if count == 2 {
			prev := lastFinished.Load()
			if prev != nil {
				secondFireIntervalCh <- now.Sub(*prev)
			}
		}
		return nil
	}, dcron.WithSinceLastSuccess(interval))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = s.Stop(stopCtx)
	}()

	select {
	case dur := <-secondFireIntervalCh:
		// dur is time between finish of run 1 and finish of run 2.
		// Since run 2 took ~workDuration, delay between finish 1 and start 2 is dur - workDuration.
		// That delay should be >= interval (150ms).
		if dur < interval {
			t.Errorf("second run finished %v after first run completion; want >= %v", dur, interval)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for second execution")
	}
}

func TestE2EMissedCatchUpOnPromotion(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := mustOpen(t)
	namespace := "e2e-missed-catchup"

	// Pre-populate history store with an execution scheduled 3 minutes ago
	st, err := store.New(db, "dcron")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	epoch, err := st.IncrementEpoch(ctx, namespace, "prior-replica")
	if err != nil {
		t.Fatalf("IncrementEpoch: %v", err)
	}

	oldScheduledAt := time.Now().Add(-3 * time.Minute).Truncate(time.Minute)
	_, err = st.Record(ctx, store.Execution{
		Namespace:   namespace,
		JobName:     "catchup-job",
		ScheduledAt: oldScheduledAt,
		StartedAt:   oldScheduledAt,
		Status:      store.StatusSuccess,
		Attempt:     1,
		InstanceID:  "prior-replica",
		LeaderEpoch: epoch,
	})
	if err != nil {
		t.Fatalf("Record prior execution: %v", err)
	}

	var catchupFired atomic.Int64
	firedCh := make(chan struct{}, 10)

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

	err = s.Add("catchup-job", "* * * * *", func(_ context.Context) error {
		catchupFired.Add(1)
		select {
		case firedCh <- struct{}{}:
		default:
		}
		return nil
	}, dcron.WithMissedRunPolicy(dcron.MissedCatchUp), dcron.WithMaxCatchUpRuns(3), dcron.WithMaxLookback(10*time.Minute))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = s.Stop(stopCtx)
	}()

	select {
	case <-firedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for catchup execution")
	}

	if catchupFired.Load() < 1 {
		t.Fatalf("catchupFired = %d; want >= 1", catchupFired.Load())
	}
}
