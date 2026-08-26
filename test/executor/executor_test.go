package executor_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/internal/executor"
)

func errSentinel(msg string) error { return errors.New(msg) }

func TestRunOK(t *testing.T) {
	t.Parallel()
	res := executor.Run(context.Background(), "test", func(context.Context) error { return nil }, executor.Retry{Attempts: 1})
	if res.Outcome != executor.OutcomeOK || res.Attempts != 1 {
		t.Fatalf("got %+v, want OK/1", res)
	}
}

func TestRunFailedNoRetry(t *testing.T) {
	t.Parallel()
	want := errSentinel("nope")
	res := executor.Run(context.Background(), "test", func(context.Context) error { return want },
		executor.Retry{Attempts: 1})
	if res.Outcome != executor.OutcomeFailed || !errors.Is(res.Error, want) || res.Attempts != 1 {
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
	res := executor.Run(context.Background(), "test", fn, executor.Retry{Attempts: 3, Backoff: time.Millisecond})
	if res.Outcome != executor.OutcomeOK || res.Attempts != 2 {
		t.Fatalf("got %+v, want OK after 2 attempts", res)
	}
}

func TestRunRetryExhausted(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fn := func(context.Context) error { calls.Add(1); return errSentinel("transient") }
	res := executor.Run(context.Background(), "test", fn, executor.Retry{Attempts: 3, Backoff: time.Millisecond})
	if res.Outcome != executor.OutcomeFailed || res.Attempts != 3 || calls.Load() != 3 {
		t.Fatalf("got %+v (calls=%d), want Failed after 3 attempts", res, calls.Load())
	}
}

func TestRunNonRetryableStops(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fn := func(context.Context) error { calls.Add(1); return errSentinel("permanent") }
	retry := executor.Retry{Attempts: 3, Backoff: time.Millisecond, Retryable: func(error) bool { return false }}
	res := executor.Run(context.Background(), "test", fn, retry)
	if res.Outcome != executor.OutcomeFailed || res.Attempts != 1 || calls.Load() != 1 {
		t.Fatalf("got %+v (calls=%d), want Failed after 1 attempt", res, calls.Load())
	}
}

func TestRunRecoversPanic(t *testing.T) {
	t.Parallel()
	res := executor.Run(context.Background(), "test", func(context.Context) error { panic("boom") }, executor.Retry{Attempts: 2})
	if res.Outcome != executor.OutcomePanicked || res.Attempts != 1 {
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
	res := executor.Run(context.Background(), "test", fn, executor.Retry{Attempts: 1, Timeout: 50 * time.Millisecond})
	if res.Outcome != executor.OutcomeTimedOut {
		t.Fatalf("got %+v, want TimedOut", res)
	}
}

func TestRunCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := executor.Run(ctx, "test", func(context.Context) error { return nil }, executor.Retry{Attempts: 1})
	if res.Outcome != executor.OutcomeCanceled {
		t.Fatalf("got %+v, want Canceled", res)
	}
}

func TestBackoff(t *testing.T) {
	r := executor.Retry{Backoff: 100 * time.Millisecond, Factor: 2, MaxBackoff: 300 * time.Millisecond}

	if got := r.Delay(1); got != 200*time.Millisecond {
		t.Fatalf("backoff(1)=%v, want 200ms", got)
	}
	if got := r.Delay(2); got != 300*time.Millisecond {
		t.Fatalf("backoff(2)=%v, want 300ms (capped)", got)
	}
	j := executor.Retry{Backoff: 100 * time.Millisecond, Jitter: true, Factor: 1}
	for i := 0; i < 20; i++ {
		d := j.Delay(1)
		if d < 75*time.Millisecond || d > 125*time.Millisecond {
			t.Fatalf("jittered backoff=%v, want [75ms,125ms]", d)
		}
	}
}

func TestGroupWaitWaitsForMembers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := executor.NewGroup()
	started := make(chan struct{})
	done := make(chan struct{})
	g.Go(ctx, "test",
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(done)
			return ctx.Err()
		}, executor.Retry{Attempts: 1}, nil)
	<-started
	cancel()
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
	g := executor.NewGroup()
	started := make(chan struct{})
	g.Go(ctx, "test",
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			time.Sleep(40 * time.Millisecond)
			return ctx.Err()
		}, executor.Retry{Attempts: 1}, nil)
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
	res := executor.Run(context.Background(), "test", func(context.Context) error { panic("boom") }, executor.Retry{Attempts: 1})
	if res.Outcome != executor.OutcomePanicked {
		t.Fatalf("Outcome = %v; want Panicked", res.Outcome)
	}
	var pe *executor.PanicError
	if !errors.As(res.Error, &pe) {
		t.Fatalf("Error = %T; want *executor.PanicError", res.Error)
	}
	if pe.Value != "boom" {
		t.Fatalf("Value = %v; want boom", pe.Value)
	}
	if len(pe.Stack) == 0 || !strings.Contains(string(pe.Stack), "executor_test.go") {
		t.Fatalf("stack must contain the panicking frame\n%s", pe.Stack)
	}
}

// TestRunTimeoutIsTyped asserts that a job which exceeded its per-attempt
// deadline surfaces as a *executor.TimeoutError (issue #26): it is errors.As-able,
// names the job, and still errors.Is(context.DeadlineExceeded) for callers
// that only know about the standard wrapping contract.
func TestRunTimeoutIsTyped(t *testing.T) {
	t.Parallel()
	fn := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	res := executor.Run(context.Background(), "billing", fn, executor.Retry{Attempts: 1, Timeout: 50 * time.Millisecond})
	if res.Outcome != executor.OutcomeTimedOut {
		t.Fatalf("Outcome = %v; want TimedOut", res.Outcome)
	}
	var te *executor.TimeoutError
	if !errors.As(res.Error, &te) {
		t.Fatalf("Error = %T; want *executor.TimeoutError", res.Error)
	}
	if te.Job != "billing" {
		t.Errorf("executor.TimeoutError.Job = %q; want %q", te.Job, "billing")
	}
	if !errors.Is(res.Error, context.DeadlineExceeded) {
		t.Errorf("executor.TimeoutError must wrap context.DeadlineExceeded")
	}
}

// TestRunPanicNamesJob asserts a recovered panic carries the job name in both
// the typed executor.PanicError.Job field and the rendered Error() string (issue #26).
func TestRunPanicNamesJob(t *testing.T) {
	t.Parallel()
	res := executor.Run(context.Background(), "billing", func(context.Context) error { panic("oom") }, executor.Retry{Attempts: 1})
	if res.Outcome != executor.OutcomePanicked {
		t.Fatalf("Outcome = %v; want Panicked", res.Outcome)
	}
	var pe *executor.PanicError
	if !errors.As(res.Error, &pe) {
		t.Fatalf("Error = %T; want *executor.PanicError", res.Error)
	}
	if pe.Job != "billing" {
		t.Errorf("executor.PanicError.Job = %q; want %q", pe.Job, "billing")
	}
	if !strings.Contains(res.Error.Error(), "billing") {
		t.Errorf("Error message = %q; want it to name the job", res.Error.Error())
	}
}
