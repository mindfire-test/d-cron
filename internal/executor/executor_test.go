package executor

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func errSentinel(msg string) error { return errors.New(msg) }

func TestRunOK(t *testing.T) {
	t.Parallel()
	res := Run(context.Background(), "test", func(context.Context) error { return nil }, Retry{Attempts: 1})
	if res.Outcome != OutcomeOK || res.Attempts != 1 {
		t.Fatalf("got %+v, want OK/1", res)
	}
}

func TestRunFailedNoRetry(t *testing.T) {
	t.Parallel()
	want := errSentinel("nope")
	res := Run(context.Background(), "test", func(context.Context) error { return want },
		Retry{Attempts: 1})
	if res.Outcome != OutcomeFailed || !errors.Is(res.Error, want) || res.Attempts != 1 {
		t.Fatalf("got %+v, want Failed with %v after 1 attempt", res, want)
	}
}

func TestRunRetryThenSuccess(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fn := func(context.Context) error {
		if calls.Add(1) < 2 {
			return errSentinel("transient")
		}
		return nil
	}
	res := Run(context.Background(), "test", fn, Retry{Attempts: 3, Backoff: time.Millisecond})
	if res.Outcome != OutcomeOK || res.Attempts != 2 {
		t.Fatalf("got %+v, want OK after 2 attempts", res)
	}
}

func TestRunRetryExhausted(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fn := func(context.Context) error { calls.Add(1); return errSentinel("transient") }
	res := Run(context.Background(), "test", fn, Retry{Attempts: 3, Backoff: time.Millisecond})
	if res.Outcome != OutcomeFailed || res.Attempts != 3 || calls.Load() != 3 {
		t.Fatalf("got %+v (calls=%d), want Failed after 3 attempts", res, calls.Load())
	}
}

func TestRunNonRetryableStops(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fn := func(context.Context) error { calls.Add(1); return errSentinel("permanent") }
	retry := Retry{Attempts: 3, Backoff: time.Millisecond, Retryable: func(error) bool { return false }}
	res := Run(context.Background(), "test", fn, retry)
	if res.Outcome != OutcomeFailed || res.Attempts != 1 || calls.Load() != 1 {
		t.Fatalf("got %+v (calls=%d), want Failed after 1 attempt", res, calls.Load())
	}
}

func TestRunRecoversPanic(t *testing.T) {
	t.Parallel()
	res := Run(context.Background(), "test", func(context.Context) error { panic("boom") }, Retry{Attempts: 2})
	if res.Outcome != OutcomePanicked || res.Attempts != 1 {
		t.Fatalf("got %+v, want Panicked after 1 attempt", res)
	}
	if res.Error == nil || !strings.Contains(res.Error.Error(), "boom") {
		t.Fatalf("panic error = %v, want it to mention boom", res.Error)
	}
}

func TestRunTimeout(t *testing.T) {
	fn := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	res := Run(context.Background(), "test", fn, Retry{Attempts: 1, Timeout: 50 * time.Millisecond})
	if res.Outcome != OutcomeTimedOut {
		t.Fatalf("got %+v, want TimedOut", res)
	}
}

func TestRunCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Run(ctx, "test", func(context.Context) error { return nil }, Retry{Attempts: 1})
	if res.Outcome != OutcomeCanceled {
		t.Fatalf("got %+v, want Canceled", res)
	}
}

func TestBackoff(t *testing.T) {
	r := Retry{Backoff: 100 * time.Millisecond, Factor: 2, MaxBackoff: 300 * time.Millisecond}
	// 100, 200, 400->300 capped
	if got := r.backoff(1); got != 200*time.Millisecond {
		t.Fatalf("backoff(1)=%v, want 200ms", got)
	}
	if got := r.backoff(2); got != 300*time.Millisecond {
		t.Fatalf("backoff(2)=%v, want 300ms (capped)", got)
	}
	j := Retry{Backoff: 100 * time.Millisecond, Jitter: true, Factor: 1}
	for i := 0; i < 20; i++ {
		d := j.backoff(1)
		if d < 75*time.Millisecond || d > 125*time.Millisecond {
			t.Fatalf("jittered backoff=%v, want [75ms,125ms]", d)
		}
	}
}

func TestGroupWaitWaitsForMembers(t *testing.T) {
	// Cancellation flows through the job context (the scheduler cancels it on
	// shutdown/demotion), and Wait only waits for the members.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := NewGroup()
	started := make(chan struct{})
	done := make(chan struct{})
	g.Go(ctx, "test",
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(done)
			return ctx.Err()
		}, Retry{Attempts: 1}, nil)
	<-started
	cancel() // shutdown signal
	if err := g.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job was not canceled when its context was canceled")
	}
}

func TestGroupWaitBoundedByDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := NewGroup()
	started := make(chan struct{})
	g.Go(ctx, "test",
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			time.Sleep(40 * time.Millisecond) // outlives the drain deadline
			return ctx.Err()
		}, Retry{Attempts: 1}, nil)
	<-started
	cancel()
	drain, dcancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer dcancel()
	err := g.Wait(drain)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait=%v, want DeadlineExceeded", err)
	}
}

func TestRunPanicIsTypedAndCarriesStack(t *testing.T) {
	t.Parallel()
	res := Run(context.Background(), "test", func(context.Context) error { panic("boom") }, Retry{Attempts: 1})
	if res.Outcome != OutcomePanicked {
		t.Fatalf("Outcome = %v; want Panicked", res.Outcome)
	}
	var pe *PanicError
	if !errors.As(res.Error, &pe) {
		t.Fatalf("Error = %T; want *PanicError", res.Error)
	}
	if pe.Value != "boom" {
		t.Fatalf("Value = %v; want boom", pe.Value)
	}
	if len(pe.Stack) == 0 || !strings.Contains(string(pe.Stack), "executor_test.go") {
		t.Fatalf("stack must contain the panicking frame\n%s", pe.Stack)
	}
}

// TestRunTimeoutIsTyped asserts that a job which exceeded its per-attempt
// deadline surfaces as a *TimeoutError (issue #26): it is errors.As-able,
// names the job, and still errors.Is(context.DeadlineExceeded) for callers
// that only know about the standard wrapping contract.
func TestRunTimeoutIsTyped(t *testing.T) {
	t.Parallel()
	fn := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	res := Run(context.Background(), "billing", fn, Retry{Attempts: 1, Timeout: 50 * time.Millisecond})
	if res.Outcome != OutcomeTimedOut {
		t.Fatalf("Outcome = %v; want TimedOut", res.Outcome)
	}
	var te *TimeoutError
	if !errors.As(res.Error, &te) {
		t.Fatalf("Error = %T; want *TimeoutError", res.Error)
	}
	if te.Job != "billing" {
		t.Errorf("TimeoutError.Job = %q; want %q", te.Job, "billing")
	}
	if !errors.Is(res.Error, context.DeadlineExceeded) {
		t.Errorf("TimeoutError must wrap context.DeadlineExceeded")
	}
}

// TestRunPanicNamesJob asserts a recovered panic carries the job name in both
// the typed PanicError.Job field and the rendered Error() string (issue #26).
func TestRunPanicNamesJob(t *testing.T) {
	t.Parallel()
	res := Run(context.Background(), "billing", func(context.Context) error { panic("oom") }, Retry{Attempts: 1})
	if res.Outcome != OutcomePanicked {
		t.Fatalf("Outcome = %v; want Panicked", res.Outcome)
	}
	var pe *PanicError
	if !errors.As(res.Error, &pe) {
		t.Fatalf("Error = %T; want *PanicError", res.Error)
	}
	if pe.Job != "billing" {
		t.Errorf("PanicError.Job = %q; want %q", pe.Job, "billing")
	}
	if !strings.Contains(res.Error.Error(), "billing") {
		t.Errorf("Error message = %q; want it to name the job", res.Error.Error())
	}
}
