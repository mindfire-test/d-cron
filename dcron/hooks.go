package dcron

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mindfire-test/d-cron/internal/executor"
)

// Hook is called on the terminal outcome of a job execution (issue #39,
// FR-409). Hooks fire asynchronously on every completion, including success;
// a hook returning an error is logged and discarded — it never propagates to
// the job or the scheduling loop. Hooks must be safe for concurrent use: they
// may fire for several jobs at once.
type Hook interface {
	// Fire receives the final Result of one logical execution.
	Fire(ctx context.Context, res executor.Result) error
}

// HookFunc adapts a plain function to the Hook interface (issue #39).
type HookFunc func(ctx context.Context, res executor.Result) error

// Fire implements Hook.
func (f HookFunc) Fire(ctx context.Context, res executor.Result) error { return f(ctx, res) }

// WithHooks registers one or more failure/success notification hooks (issue
// #39, FR-409). Hooks are invoked asynchronously after each execution
// completes; a hook error is logged and never fails the job.
func WithHooks(hooks ...Hook) Option {
	return func(o *options) { o.hooks = append(o.hooks, hooks...) }
}

// fireHooks dispatches the result of a completed job to every registered hook
// (issue #39) on the executor group. Hook failures are logged, not propagated.
// Each hook runs on its own goroutine (bounded by runCtx, which is cancelled on
// shutdown), so a slow webhook cannot stall the loop or other hooks.
func (s *Scheduler) fireHooks(res executor.Result) {
	s.mu.Lock()
	hooks := s.opts.hooks
	runCtx := s.runCtx
	s.mu.Unlock()
	for _, h := range hooks {
		h := h
		s.group.Go(runCtx, "_hook:"+res.Name, executor.Func(func(ctx context.Context) error {
			return h.Fire(ctx, res)
		}), executor.Retry{Attempts: 1}, s.opts.logger)
	}
}

// webhookPayload is the JSON body sent to a WebhookHook (issue #39).
type webhookPayload struct {
	Job        string `json:"job"`
	Outcome    string `json:"outcome"`
	Attempts   int    `json:"attempts"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Time       string `json:"time"`
}

// WebhookHook notifies an HTTP endpoint with a JSON POST on terminal job
// outcome (issue #39). The request context is derived from the job's own
// shutdown context, so it is cancelled on scheduler shutdown. Timeout bounds a
// single HTTP round-trip; the client is reused across calls.
type WebhookHook struct {
	// URL is the endpoint receiving POSTs.
	URL string
	// Timeout bounds a single delivery attempt. Defaults to 5s when zero.
	Timeout time.Duration
	// Client is the HTTP client used for deliveries. Defaults to a fresh
	// client when nil.
	Client *http.Client
	// Headers are added to every request, e.g. an auth bearer token.
	Headers map[string]string
}

// Fire implements Hook by POSTing the outcome as JSON (issue #39). Errors
// from the request (including a non-2xx status) are returned so the scheduler
// can log them; they never propagate to the job.
func (w *WebhookHook) Fire(ctx context.Context, res executor.Result) error {
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := w.Client
	if client == nil {
		// Use the process-wide default client; per-call timeouts are applied via
		// the request context below so a dead endpoint honours the budget.
		client = http.DefaultClient
	}
	body := webhookPayload{
		Job:        res.Name,
		Outcome:    res.Outcome.String(),
		Attempts:   res.Attempts,
		DurationMS: res.Duration.Milliseconds(),
		Time:       time.Now().UTC().Format(time.RFC3339),
	}
	if res.Error != nil {
		body.Error = res.Error.Error()
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("dcron: webhook hook: marshal payload: %w", err)
	}
	// Bound the whole round-trip by both the caller's shutdown context and the
	// per-delivery timeout so a slow endpoint never outlives its budget.
	reqCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, w.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("dcron: webhook hook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dcron: webhook hook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("dcron: webhook hook: unexpected status %d", resp.StatusCode)
	}
	return nil
}

var (
	_ Hook = HookFunc(nil)
	_ Hook = (*WebhookHook)(nil)
)
