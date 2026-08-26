package dcron_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/internal/executor"
	"github.com/mindfire-test/d-cron/metrics"
)

// recordingRecorder is a metrics.Recorder that records a bounded set of signals
// for asserting that the core emits them (#36).
type recordingRecorder struct {
	leaders     atomic.Int64
	started     atomic.Int64
	finished    atomic.Int64
	lastJob     atomic.Value
	lastOutcome atomic.Value
	lastDone    atomic.Bool
}

func newRecordingRecorder() *recordingRecorder {
	r := &recordingRecorder{}
	r.lastJob.Store("")
	r.lastOutcome.Store("")
	return r
}

func (r *recordingRecorder) SetLeader(_ string, isLeader bool) {
	if isLeader {
		r.leaders.Add(1)
	}
}

func (r *recordingRecorder) LeaderTransition(_ string) {}

func (r *recordingRecorder) JobStarted(_ string) { r.started.Add(1) }

func (r *recordingRecorder) JobFinished(job string, out metrics.Outcome, _ time.Duration, _ bool) {
	r.finished.Add(1)
	r.lastJob.Store(job)
	r.lastOutcome.Store(out.String())
	r.lastDone.Store(true)
}

func (r *recordingRecorder) FencedWrite()       {}
func (r *recordingRecorder) MissedRun(_ string) {}

func waitFor(cond func() bool, what string) {
	deadline := time.Now().Add(4 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		panic("timed out waiting for " + what)
	}
}

func TestAddOnceEvictsAfterFiring(t *testing.T) {
	s := testScheduler(newSchedBackend())
	var fired atomic.Int64
	at := time.Now().Add(500 * time.Millisecond)
	if err := s.AddOnce("wallclock", at, func(context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("AddOnce: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return fired.Load() >= 1 }, "once-job to fire")
	time.Sleep(400 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if got := fired.Load(); got != 1 {
		t.Fatalf("once job fired %d times; want exactly 1", got)
	}
}

func TestLeadershipThreeState(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if got := s.Leadership(); got != dcron.LeadershipUnknown {
		t.Fatalf("pre-start Leadership = %v; want unknown", got)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return s.Leadership() == dcron.LeadershipLeader }, "leadership to promote")
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHealthCheckNilDBPassthrough(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck with no db: %v", err)
	}
}

func TestJobsReportsRunOutcome(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.Add("ok", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool {
		st := s.Jobs()
		return len(st) == 1 && st[0].LastOutcome == "ok"
	}, "job outcome")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}

func TestJobsReportsRunningFlag(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.Add("slow", "@every 2ms", func(_ context.Context) error {
		for i := 0; i < 30_000_000; i++ {
			_ = i
		} // busy loop, ignores ctx
		return nil
	}, dcron.WithTimeout(10*time.Minute), dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool {
		st := s.Jobs()
		return len(st) == 1 && st[0].Running
	}, "job running flag")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}

func TestHooksInvokedOnCompletion(t *testing.T) {
	var fired atomic.Int64
	hook := dcron.HookFunc(func(_ context.Context, _ executor.Result) error {
		fired.Add(1)
		return nil
	})
	s := testScheduler(newSchedBackend(), dcron.WithHooks(hook))
	if err := s.Add("henr", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return fired.Load() >= 1 }, "hook to fire")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if fired.Load() == 0 {
		t.Fatal("hook was not invoked")
	}
}

func TestMetricsRecorderReceivesSignals(t *testing.T) {
	rec := newRecordingRecorder()
	s := testScheduler(newSchedBackend(), dcron.WithMetrics(rec))
	if err := s.Add("job", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return rec.lastDone.Load() }, "job finished")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if rec.leaders.Load() == 0 {
		t.Fatalf("expected SetLeader(instance,true), saw %d", rec.leaders.Load())
	}
	if rec.started.Load() == 0 || rec.finished.Load() == 0 {
		t.Fatalf("expected JobStarted/JobFinished, got %d/%d", rec.started.Load(), rec.finished.Load())
	}
	if job, _ := rec.lastJob.Load().(string); job != "job" {
		t.Fatalf("metrics JobFinished job = %q; want job", job)
	}
}

func TestWebhookHookConstruction(t *testing.T) {
	w := dcron.WebhookHook{URL: "http://localhost:1/x"}
	if w.URL != "http://localhost:1/x" {
		t.Fatalf("URL = %q", w.URL)
	}
	// A refused connection errors (no live server); asserts the plumbing is
	// wired and errors are returned, never thrown.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = w.Fire(ctx, executor.Result{Name: "j", Outcome: executor.OutcomeFailed})
	http.DefaultClient.CloseIdleConnections()
}

func errIs(err, target error) bool { return errors.Is(err, target) }
func TestAddOnceRejectsPastTime(t *testing.T) {
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }
	past := time.Now().Add(-time.Minute)
	if err := s.AddOnce("expired", past, noop); !errIs(err, dcron.ErrInvalidSpec) {
		t.Fatalf("AddOnce with past time err = %v; want dcron.ErrInvalidSpec", err)
	}
}

func TestAddOnceDuplicateName(t *testing.T) {
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }
	at := time.Now().Add(time.Hour)
	if err := s.AddOnce("dup", at, noop); err != nil {
		t.Fatalf("AddOnce: %v", err)
	}
	if err := s.AddOnce("dup", at.Add(time.Hour), noop); !errIs(err, dcron.ErrJobExists) {
		t.Fatalf("duplicate AddOnce err = %v; want dcron.ErrJobExists", err)
	}
}
