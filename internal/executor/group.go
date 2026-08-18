package executor

import (
	"context"
	"log/slog"
	"sync"
)

// Group tracks a set of concurrently-running jobs so they can be drained
// together with a bounded timeout. Each Go call runs fn under the group's
// context, which is cancelled by Wait.
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewGroup returns a Group whose member jobs are cancelled when Wait is
// called (or when parent is canceled).
func NewGroup(parent context.Context) *Group {
	ctx, cancel := context.WithCancel(parent)
	return &Group{ctx: ctx, cancel: cancel}
}

// Go runs fn under the group. The returned outcome is logged on failure; the
// call does not block.
func (g *Group) Go(fn Func, retry Retry, log *slog.Logger) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		res := Run(g.ctx, fn, retry)
		if res.Outcome != OutcomeOK && log != nil {
			log.Error(
				"executor: job failed",
				"outcome", res.Outcome,
				"err", res.Error,
				"attempts", res.Attempts,
			)
		}
	}()
}

// Wait cancels all member jobs and blocks until they finish or until the
// drain deadline (ctx.Done) elapses. It returns the drain error on timeout.
func (g *Group) Wait(ctx context.Context) error {
	g.cancel()
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
