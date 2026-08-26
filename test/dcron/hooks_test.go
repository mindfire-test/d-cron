package dcron_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/internal/executor"
)

func TestHooksInvokedOnCompletion(t *testing.T) {
	var fired atomic.Int64
	hook := dcron.HookFunc(func(_ context.Context, _ executor.Result) error {
		fired.Add(1)
		return nil
	})
	s := testScheduler(newSchedBackend(), dcron.WithHooks(hook))
	if err := s.Add("henr", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return fired.Load() >= 1 }, "hook to fire")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if fired.Load() == 0 {
		t.Fatal("hook was not invoked")
	}
}

func TestWebhookHookConstruction(t *testing.T) {
	w := dcron.WebhookHook{URL: "http://localhost:1/x"}
	if w.URL != "http://localhost:1/x" {
		t.Fatalf("URL = %q", w.URL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = w.Fire(ctx, executor.Result{Name: "j", Outcome: executor.OutcomeFailed})
	http.DefaultClient.CloseIdleConnections()
}
