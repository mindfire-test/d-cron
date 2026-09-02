package executor

import (
	"context"
	"errors"
	"runtime"
	"time"
)

type attemptResult struct {
	val   any
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
		default:
			if attempt < r.Attempts && r.Retryable(err) {
				sleep(ctx, r.Delay(attempt))
				continue
			}
		}
		return Result{Name: name, Outcome: out, Error: wrapJobErr(name, out, err), Attempts: attempt, Duration: time.Since(start)}
	}
}

func wrapJobErr(name string, out Outcome, err error) error {
	if err == nil {
		return nil
	}
	if out == OutcomeTimedOut && errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{Job: name, Err: err}
	}
	return err
}

func attemptCtx(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

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

func runAttempt(ctx context.Context, name string, fn Func) (Outcome, []byte, error) {
	ch := make(chan attemptResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil { // issue #18, FR-302: deferred recover() boundary
				ch <- attemptResult{
					val:   r,
					stack: stackDump(), // captures debug.Stack() before unwinding
					panic: true,
				}
			}
		}()
		ch <- attemptResult{err: fn(ctx)}
	}()

	res := <-ch
	if res.panic {
		return OutcomePanicked, res.stack, &PanicError{Job: name, Value: res.val, Stack: res.stack}
	}
	return classify(res.err), nil, res.err
}

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
