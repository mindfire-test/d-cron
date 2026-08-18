package dcron

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// schedBackend is a minimal elector.Backend that always promotes its caller to
// leader and thereafter confirms ownership, so a scheduler wired to it runs
// jobs for as long as it stays started.
type schedBackend struct {
	mu   sync.Mutex
	held bool
}

func newSchedBackend() *schedBackend { return &schedBackend{} }

func (b *schedBackend) TryLock(ctx context.Context, _ int64) (bool, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	if b.held {
		return false, 0, nil
	}
	b.held = true
	return true, 1, nil
}

func (b *schedBackend) HoldsLock(ctx context.Context, _ int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return b.held, nil
}

func (b *schedBackend) Release(ctx context.Context, _ int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	b.held = false
	return nil
}

func (b *schedBackend) Close() error { return nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testCfg() options {
	cfg := defaultOptions()
	cfg.pollInterval = 5 * time.Millisecond
	cfg.drainTimeout = 2 * time.Second
	cfg.logger = discardLogger()
	return cfg
}

func TestNewNilDB(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNilDB) {
		t.Fatalf("New(nil) err = %v; want ErrNilDB", err)
	}
}

func TestAddValidation(t *testing.T) {
	s := newWithBackend(newSchedBackend(), testCfg())
	noop := func(context.Context) error { return nil }

	if err := s.Add("bad-spec", "not a spec", noop); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("bad spec err = %v; want ErrInvalidSpec", err)
	}
	if err := s.Add("nil-fn", "*/5 * * * *", nil); !errors.Is(err, ErrNilJob) {
		t.Fatalf("nil fn err = %v; want ErrNilJob", err)
	}
	if err := s.Add("tick", "*/5 * * * *", noop); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add("tick", "*/5 * * * *", noop); !errors.Is(err, ErrJobExists) {
		t.Fatalf("duplicate err = %v; want ErrJobExists", err)
	}
}

func TestSchedulerLifecycle(t *testing.T) {
	s := newWithBackend(newSchedBackend(), testCfg())
	if err := s.Stop(context.Background()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Stop before Start err = %v; want ErrNotStarted", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start err = %v; want ErrAlreadyStarted", err)
	}
	if err := s.Add("late", "*/5 * * * *", func(context.Context) error { return nil }); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("Add after Start err = %v; want ErrAlreadyStarted", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := s.Stop(context.Background()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Stop after Stop err = %v; want ErrNotStarted", err)
	}
}

func TestSchedulerFiresJobWhenLeader(t *testing.T) {
	s := newWithBackend(newSchedBackend(), testCfg())
	var fired atomic.Int64
	if err := s.Add("tick", "@every 1ms", func(context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for fired.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := fired.Load(); got < 3 {
		t.Fatalf("job fired %d times; want at least 3", got)
	}
}

func TestSchedulerInjectsEpochAndIdempotencyKey(t *testing.T) {
	s := newWithBackend(newSchedBackend(), testCfg())
	var epochSeen atomic.Int64
	var keySeen atomic.Value
	jobName := "report"
	if err := s.Add(jobName, "@every 1ms", func(ctx context.Context) error {
		epochSeen.Store(Epoch(ctx))
		keySeen.Store(IdempotencyKey(ctx))
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for epochSeen.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if epochSeen.Load() == 0 {
		t.Fatalf("leader epoch was not injected into the job context")
	}
	if key, _ := keySeen.Load().(string); key != jobName {
		t.Fatalf("idempotency key = %q; want %q", key, jobName)
	}
}
