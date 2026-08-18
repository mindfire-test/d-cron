package executor

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
)

type attemptResult struct {
	err   error
	stack []byte
	panic bool
}

// Run executes fn, retrying per retry. It recovers panics so a panicking job
// never crashes the host, honours fn's context for timeout/cancellation, and
// backs off between retries.
func Run(ctx context.Context, fn Func, retry Retry) Result {
	r := retry.withDefaults()
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return Result{Outcome: outcomeOf(err), Error: err, Attempts: attempt - 1}
		}
		ac, cancel := attemptCtx(ctx, r.Timeout)
		out, _, err := runAttempt(ac, fn)
		cancel()
		switch out {
		case OutcomeOK:
			return Result{Outcome: OutcomeOK, Attempts: attempt}
		case OutcomePanicked:
			return Result{Outcome: OutcomePanicked, Error: err, Attempts: attempt}
		case OutcomeCanceled:
			return Result{Outcome: OutcomeCanceled, Error: err, Attempts: attempt}
		case OutcomeTimedOut:
			if attempt < r.Attempts {
				sleep(ctx, r.backoff(attempt))
				continue
			}
		default: // OutcomeFailed
			if attempt < r.Attempts && r.Retryable(err) {
				sleep(ctx, r.backoff(attempt))
				continue
			}
		}
		return Result{Outcome: out, Error: err, Attempts: attempt}
	}
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
func runAttempt(ctx context.Context, fn Func) (Outcome, []byte, error) {
	ch := make(chan attemptResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- attemptResult{
					err:   fmt.Errorf("executor: recovered panic: %v", r),
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
		return OutcomePanicked, res.stack, res.err
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
