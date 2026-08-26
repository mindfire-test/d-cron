package executor

import (
	"context"
	"errors"
	"runtime"
	"time"
)

type attemptResult struct {
	val   any // panic value when panicked
	err   error
	stack []byte
	panic bool
}

// Run executes fn, retrying per retry. It recovers panics so a panicking job
// never crashes the host, honours fn's context for timeout/cancellation, and
// backs off between retries.
//
// Limitation (SDS §5.1 / issue #18): only a panic on the job's own goroutine is
// recovered and turned into a *PanicError. A panic on a goroutine the job
// itself spawns cannot be recovered by Go and will terminate the process; the
// job-authoring guide (#30) requires jobs to recover inside any goroutine they
// spawn.
func Run(ctx context.Context, name string, fn Func, retry Retry) Result {
	r := retry.withDefaults()
	start := time.Now()
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			out := outcomeOf(err)
			return Result{Name: name, Outcome: out, Error: wrapJobErr(name, out, err), Attempts: attempt - 1, Duration: time.Since(start)}
		}
		ac, cancel := attemptCtx(ctx, r.Timeout)
		out, _, err := runAttempt(ac, name, fn)
		cancel()
		switch out {
		case OutcomeOK:
			return Result{Name: name, Outcome: OutcomeOK, Attempts: attempt, Duration: time.Since(start)}
		case OutcomePanicked:
			return Result{Name: name, Outcome: OutcomePanicked, Error: err, Attempts: attempt, Duration: time.Since(start)}
		case OutcomeCanceled:
			return Result{Name: name, Outcome: OutcomeCanceled, Error: err, Attempts: attempt, Duration: time.Since(start)}
		case OutcomeTimedOut:
			if attempt < r.Attempts {
				sleep(ctx, r.Delay(attempt))
				continue
			}
		default: // OutcomeFailed
			if attempt < r.Attempts && r.Retryable(err) {
				sleep(ctx, r.Delay(attempt))
				continue
			}
		}
		return Result{Name: name, Outcome: out, Error: wrapJobErr(name, out, err), Attempts: attempt, Duration: time.Since(start)}
	}
}

// wrapJobErr tags a result error with the job name (issue #26) and wraps
// deadline failures as *TimeoutError so callers can errors.Is(err,
// context.DeadlineExceeded) while still distinguishing a job timeout. Canceled
// and failed errors are returned as-is for backward-compatible errors.Is.
func wrapJobErr(name string, out Outcome, err error) error {
	if err == nil {
		return nil
	}
	if out == OutcomeTimedOut && errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{Job: name, Err: err}
	}
	return err
}

// attemptCtx bounds a single run by the per-attempt timeout, or derives a
// cancellable context when none was requested so Run can cancel on completion.
func attemptCtx(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

// sleep pauses for d unless ctx is canceled first.
func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// runAttempt runs fn once under ctx, capturing any panic. It never returns
// OutcomeUnknown.
func runAttempt(ctx context.Context, name string, fn Func) (Outcome, []byte, error) {
	ch := make(chan attemptResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// Capture the stack inside the deferred func, before the stack
				// unwinds further: a post-unwind capture loses the panicking
				// frame (issue #18).
				ch <- attemptResult{
					val:   r,
					stack: stackDump(),
					panic: true,
				}
			}
		}()
		ch <- attemptResult{err: fn(ctx)}
	}()
	// Join the in-flight job goroutine: block until fn returns so callers
	// (notably Group.Wait) observe real completion rather than the instant
	// the context cancels. A cooperative job honours ctx -- its per-attempt
	// deadline or the scheduler's shutdown -- so this normally returns
	// promptly. A job that ignores ctx cannot be pre-empted in Go; it is
	// bounded instead by the scheduler's drain deadline (Group.Wait) and
	// logged as orphaned. See SDS §7.
	res := <-ch
	if res.panic {
		return OutcomePanicked, res.stack, &PanicError{Job: name, Value: res.val, Stack: res.stack}
	}
	return classify(res.err), nil, res.err
}

// classify maps a returned error to the matching non-OK outcome.
func classify(err error) Outcome {
	if err == nil {
		return OutcomeOK
	}
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimedOut
	}
	return OutcomeFailed
}

func outcomeOf(err error) Outcome {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled
	}
	return OutcomeTimedOut
}

// stackDump returns the calling goroutine's stack trace.
func stackDump() []byte {
	buf := make([]byte, 1024)
	for {
		n := runtime.Stack(buf, false)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}
