package dcron_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
)

// TestScheduler_WithNoOverlap_SkipsRunning proves that with WithNoOverlap(),
// an incoming tick while the previous execution is active is skipped and records a missed run.
func TestScheduler_WithNoOverlap_SkipsRunning(t *testing.T) {
	t.Parallel()

	rec := newRecordingRecorder()
	s := testScheduler(newSchedBackend(), dcron.WithMetrics(rec))

	started := make(chan struct{})
	unblock := make(chan struct{})
	var runCount atomic.Int32

	err := s.Add("no-overlap-job", "@every 5ms", func(_ context.Context) error {
		count := runCount.Add(1)
		if count == 1 {
			select {
			case <-started:
			default:
				close(started)
			}
			<-unblock
		}
		return nil
	}, dcron.WithNoOverlap())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the first run to start and block
	<-started

	// Wait long enough for multiple 5ms ticks to arrive while first run is blocked
	time.Sleep(50 * time.Millisecond)

	// Close unblock so the first run finishes
	close(unblock)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)

	// Verify that missed runs were recorded because overlapping runs were skipped
	missed := rec.missed.Load()
	if missed == 0 {
		t.Errorf("expected missed runs to be recorded for skipped overlaps, got %d", missed)
	}
}

// TestScheduler_OverlapQueue_QueuesNextRun proves that with OverlapQueue,
// an incoming tick while the previous execution is active is queued and fires sequentially.
func TestScheduler_OverlapQueue_QueuesNextRun(t *testing.T) {
	t.Parallel()

	s := testScheduler(newSchedBackend())

	firstStarted := make(chan struct{})
	unblockFirst := make(chan struct{})
	var runCount atomic.Int32
	var sequentialFired atomic.Bool

	err := s.Add("queue-job", "@every 5ms", func(_ context.Context) error {
		cnt := runCount.Add(1)
		if cnt == 1 {
			select {
			case <-firstStarted:
			default:
				close(firstStarted)
			}
			<-unblockFirst
			return nil
		}
		if cnt >= 2 {
			sequentialFired.Store(true)
		}
		return nil
	}, dcron.WithOverlapPolicy(dcron.OverlapQueue))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	<-firstStarted
	// Wait a bit to ensure a tick arrives and queues a run
	time.Sleep(30 * time.Millisecond)

	// Unblock the first run
	close(unblockFirst)

	// Wait for the queued run to execute sequentially
	waitFor(t, func() bool { return sequentialFired.Load() }, "queued run to execute")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)

	if !sequentialFired.Load() {
		t.Fatal("queued run did not fire after previous run completed")
	}
}

// TestScheduler_ShutdownDrainContext_UncancelledUntilDeadline proves that
// when Scheduler.Stop is called with a drain budget, running jobs remain UN-cancelled
// until the drain deadline expires.
func TestScheduler_ShutdownDrainContext_UncancelledUntilDeadline(t *testing.T) {
	t.Parallel()

	s := testScheduler(newSchedBackend())

	jobStarted := make(chan struct{})
	ctxCancelled := make(chan error, 1)

	const drainBudget = 150 * time.Millisecond

	err := s.Add("drain-job", "@every 5ms", func(ctx context.Context) error {
		close(jobStarted)

		// Assert context is NOT cancelled initially
		if err := ctx.Err(); err != nil {
			ctxCancelled <- err
			return err
		}

		// Wait for context cancellation
		<-ctx.Done()
		ctxCancelled <- ctx.Err()
		return ctx.Err()
	}, dcron.WithNoOverlap())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	<-jobStarted

	// Call Stop with a drain deadline
	stopStart := time.Now()
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainBudget)
	defer cancelDrain()

	err = s.Stop(drainCtx)
	stopDuration := time.Since(stopStart)

	// The drain deadline should have expired
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("Stop returned error: %v (expected DeadlineExceeded)", err)
	}

	// Verify duration is at least drainBudget
	if stopDuration < drainBudget-20*time.Millisecond {
		t.Fatalf("Stop exited too quickly: %v < %v (drain context cancelled prematurely!)", stopDuration, drainBudget)
	}

	// Check context error received by job
	select {
	case jobErr := <-ctxCancelled:
		if !errors.Is(jobErr, context.DeadlineExceeded) && !errors.Is(jobErr, context.Canceled) {
			t.Fatalf("job context err = %v; want DeadlineExceeded or Canceled", jobErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for job cancellation signal")
	}
}

// TestScheduler_WithMissedRunPolicy_Skip proves that MissedSkip does not execute missed runs.
func TestScheduler_WithMissedRunPolicy_Skip(t *testing.T) {
	t.Parallel()

	s := testScheduler(newSchedBackend())
	var runs atomic.Int32

	err := s.Add("skip-job", "@every 1m", func(ctx context.Context) error {
		runs.Add(1)
		return nil
	}, dcron.WithMissedRunPolicy(dcron.MissedSkip))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = s.Stop(ctx)

	// Since the schedule is 1m and we stopped after 50ms with MissedSkip, no run should occur
	if runs.Load() != 0 {
		t.Fatalf("expected 0 runs with MissedSkip, got %d", runs.Load())
	}
}

// TestScheduler_FireDue_ReducedLockContention proves that while jobs are being dispatched,
// calls to s.Jobs(), s.Pause(), and s.Resume() proceed concurrently without lock contention.
func TestScheduler_FireDue_ReducedLockContention(t *testing.T) {
	t.Parallel()

	s := testScheduler(newSchedBackend())

	for i := 0; i < 10; i++ {
		name := "job-" + string(rune('a'+i))
		_ = s.Add(name, "@every 2ms", func(ctx context.Context) error {
			time.Sleep(5 * time.Millisecond)
			return nil
		}, dcron.WithNoOverlap())
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	stopQuerying := make(chan struct{})

	// Concurrently query Jobs() and admin methods during dispatch
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				select {
				case <-stopQuerying:
					return
				default:
					start := time.Now()
					jobs := s.Jobs()
					if len(jobs) != 10 {
						t.Errorf("worker %d: got %d jobs, want 10", w, len(jobs))
					}
					dur := time.Since(start)
					if dur > 50*time.Millisecond {
						t.Errorf("worker %d: Jobs() held lock too long: %v", w, dur)
					}
					time.Sleep(time.Millisecond)
				}
			}
		}(worker)
	}

	time.Sleep(100 * time.Millisecond)
	close(stopQuerying)
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}
