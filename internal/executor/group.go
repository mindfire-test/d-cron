package executor

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Group tracks a set of concurrently-running jobs so they can be drained
// together with a bounded timeout.
//
// Cancellation is NOT owned by the group: each Go call receives the context the
// job runs under, so the scheduler can cancel on shutdown (runCtx) and on
// demotion (termCtx) separately (FR-307). Wait only waits, up to a deadline.
type Group struct {
	wg sync.WaitGroup
}

// NewGroup returns an empty Group.
func NewGroup() *Group {
	return &Group{}
}

// Go runs fn under ctx, retrying per retry. The returned outcome is logged on
// failure, including the recovered-panic stack when applicable. The call does
// not block. ctx must carry shutdown and demotion cancellation (SDS §5.3).
// onComplete, if non-nil, is invoked with the final Result on the caller's
// goroutine so the scheduler can update job status, fire hooks, and record
// metrics without coupling the executor to those concerns.
func (g *Group) Go(ctx context.Context, name string, fn Func, retry Retry, log *slog.Logger, onComplete ...func(Result)) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		res := Run(ctx, name, fn, retry)
		if res.Outcome != OutcomeOK && log != nil {
			attrs := []any{
				"job", name,
				"outcome", res.Outcome,
				"err", res.Error,
				"attempts", res.Attempts,
			}
			var pe *PanicError
			if errors.As(res.Error, &pe) && len(pe.Stack) > 0 {
				attrs = append(attrs, "stack", string(pe.Stack))
			}
			log.Error("executor: job failed", attrs...)
		}
		for _, cb := range onComplete {
			if cb != nil {
				cb(res)
			}
		}
	}()
}

// Wait blocks until every member job finishes, or until the drain deadline
// (ctx.Done) elapses. It returns nil on completion and the deadline error on
// timeout; a timeout does not cancel jobs (the scheduler's context handles
// that, see Group's doc comment).
func (g *Group) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
