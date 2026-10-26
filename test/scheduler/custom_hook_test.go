package dcron_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
)

// customHook proves that an external package can implement dcron.Hook
// without importing internal/executor.
type customHook struct {
	mu      sync.Mutex
	results []dcron.Result
}

func (c *customHook) Fire(_ context.Context, res dcron.Result) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, res)
	return nil
}

func TestExternalCustomHookImplementation(t *testing.T) {
	hook := &customHook{}
	backend := newSchedBackend()
	s := testScheduler(backend, dcron.WithPollInterval(5*time.Millisecond), dcron.WithHooks(hook))

	if err := s.Add("ext-hook-job", "@every 1ms", func(context.Context) error {
		return nil
	}, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(func() bool {
		hook.mu.Lock()
		defer hook.mu.Unlock()
		return len(hook.results) >= 1
	}, "custom hook to receive result")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)

	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.results) == 0 {
		t.Fatal("expected hook results, got none")
	}
	r := hook.results[0]
	if r.Name != "ext-hook-job" {
		t.Errorf("got job name %q, want ext-hook-job", r.Name)
	}
	if r.Outcome != "ok" {
		t.Errorf("got outcome %q, want ok", r.Outcome)
	}
	if r.Attempts != 1 {
		t.Errorf("got attempts %d, want 1", r.Attempts)
	}
}
