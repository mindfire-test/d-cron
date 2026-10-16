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

// Hook is called on the terminal outcome of a job execution.
type Hook interface {
	// Fire receives the final Result of one logical execution.
	Fire(ctx context.Context, res executor.Result) error
}

// HookFunc adapts a plain function to the Hook interface.
type HookFunc func(ctx context.Context, res executor.Result) error

// Fire implements Hook.
func (f HookFunc) Fire(ctx context.Context, res executor.Result) error { return f(ctx, res) }

// WithHooks registers one or more failure/success notification hooks.
func WithHooks(hooks ...Hook) Option {
	return func(o *options) { o.hooks = append(o.hooks, hooks...) }
}

func (s *Scheduler) fireHooks(res executor.Result) {
	s.mu.Lock()
	hooks := s.opts.hooks
	runCtx := s.runCtx
	s.mu.Unlock()
	for _, h := range hooks {
		s.group.Go(runCtx, "_hook:"+res.Name, executor.Func(func(ctx context.Context) error {
			return h.Fire(ctx, res)
		}), executor.Retry{Attempts: 1}, s.opts.logger)
	}
}

type webhookPayload struct {
	Job        string `json:"job"`
	Outcome    string `json:"outcome"`
	Attempts   int    `json:"attempts"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Time       string `json:"time"`
}

// WebhookHook notifies an HTTP endpoint with a JSON POST on terminal job outcome.
type WebhookHook struct {
	URL     string
	Timeout time.Duration
	Client  *http.Client
	Headers map[string]string
}

// Fire implements Hook by POSTing the outcome as JSON.
func (w *WebhookHook) Fire(ctx context.Context, res executor.Result) error {
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := w.Client
	if client == nil {
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
