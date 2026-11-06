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
// Limitation: only a panic on the job's own goroutine is recovered and turned
// into a *PanicError. A panic on a goroutine the job itself spawns cannot be
// recovered by Go runtime mechanics and will terminate the process; job
// implementations must recover inside any sub-goroutines they spawn.
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

// attemptCtx applies a per-attempt execution timeout using context.WithTimeout.
// Defaults to 30 minutes, configurable per job via WithTimeout. Exceeding timeout cancels the job context.
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

// runAttempt executes fn on a dedicated goroutine protected by a deferred recover.
// It reports outcomes promptly if fn completes, panics, or if ctx is canceled/times out.
//
// Limitation: Go runtime mechanics do not support forcibly terminating a running goroutine.
// If a job ignores context cancellation (e.g. busy loops or un-cancellable blocking calls),
// its goroutine will continue executing until it terminates naturally or the process exits.
// However, runAttempt unblocks immediately upon context cancellation or timeout so that
// the scheduler does not hang.
func runAttempt(ctx context.Context, name string, fn Func) (Outcome, []byte, error) {
	ch := make(chan attemptResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil { // deferred recover() boundary protects host process
				ch <- attemptResult{
					val:   r,
					stack: stackDump(), // captures debug.Stack() before unwinding
					panic: true,
				}
			}
		}()
		ch <- attemptResult{err: fn(ctx)}
	}()

	select {
	case res := <-ch:
		if res.panic {
			return OutcomePanicked, res.stack, &PanicError{Job: name, Value: res.val, Stack: res.stack}
		}
		return classify(res.err), nil, res.err
	case <-ctx.Done():
		// Check if the job finished just as the context expired to preserve its outcome/error.
		select {
		case res := <-ch:
			if res.panic {
				return OutcomePanicked, res.stack, &PanicError{Job: name, Value: res.val, Stack: res.stack}
			}
			return classify(res.err), nil, res.err
		default:
			return classify(ctx.Err()), nil, ctx.Err()
		}
	}
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
