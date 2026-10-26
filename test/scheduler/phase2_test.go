package dcron_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/metrics"
)

type recordingRecorder struct {
	leaders     atomic.Int64
	started     atomic.Int64
	finished    atomic.Int64
	missed      atomic.Int64
	lastJob     atomic.Value
	lastOutcome atomic.Value
	lastDone    atomic.Bool
}

func newRecordingRecorder() *recordingRecorder {
	r := &recordingRecorder{}
	r.lastJob.Store("")
	r.lastOutcome.Store("")
	return r
}

func (r *recordingRecorder) SetLeader(_ string, isLeader bool) {
	if isLeader {
		r.leaders.Add(1)
	}
}

func (r *recordingRecorder) LeaderTransition(_ string) {}

func (r *recordingRecorder) JobStarted(_ string) { r.started.Add(1) }

func (r *recordingRecorder) JobFinished(job string, out metrics.Outcome, _ time.Duration, _ bool) {
	r.finished.Add(1)
	r.lastJob.Store(job)
	r.lastOutcome.Store(out.String())
	r.lastDone.Store(true)
}

func (r *recordingRecorder) FencedWrite()       {}
func (r *recordingRecorder) MissedRun(_ string) { r.missed.Add(1) }

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestAddOnceEvictsAfterFiring(t *testing.T) {
	s := testScheduler(newSchedBackend())
	var fired atomic.Int64
	at := time.Now().Add(50 * time.Millisecond)
	if err := s.AddOnce("wallclock", at, func(context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("AddOnce: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fired.Load() >= 1 }, "once-job to fire")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if got := fired.Load(); got != 1 {
		t.Fatalf("once job fired %d times; want exactly 1", got)
	}
}

func TestLeadershipThreeState(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if got := s.Leadership(); got != dcron.LeadershipUnknown {
		t.Fatalf("pre-start Leadership = %v; want unknown", got)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Leadership() == dcron.LeadershipLeader }, "leadership to promote")
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHealthCheckNilDBPassthrough(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck with no db: %v", err)
	}
}

type pingableTestBackend struct {
	schedBackend
	pingErr error
}

func (p *pingableTestBackend) Ping(_ context.Context) error {
	return p.pingErr
}

func TestHealthCheck_DedicatedLockConnectionBroken(t *testing.T) {
	backend := &pingableTestBackend{pingErr: errors.New("broken lock socket")}
	s := testScheduler(backend)

	err := s.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("HealthCheck expected error for broken dedicated lock connection, got nil")
	}
	if !strings.Contains(err.Error(), "dedicated lock connection") {
		t.Fatalf("HealthCheck error = %v; want dedicated lock connection mention", err)
	}
}

func TestHealthCheck_DedicatedLockConnectionHealthy(t *testing.T) {
	backend := &pingableTestBackend{pingErr: nil}
	s := testScheduler(backend)

	if err := s.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck expected nil error, got: %v", err)
	}
}

func TestJobsReportsRunOutcome(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.Add("ok", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st := s.Jobs()
		return len(st) == 1 && st[0].LastOutcome == "ok"
	}, "job outcome")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}

func TestJobsReportsRunningFlag(t *testing.T) {
	s := testScheduler(newSchedBackend())
	started := make(chan struct{})
	unblock := make(chan struct{})

	if err := s.Add("slow", "@every 2ms", func(_ context.Context) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-unblock
		return nil
	}, dcron.WithTimeout(10*time.Minute), dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	waitFor(t, func() bool {
		st := s.Jobs()
		return len(st) == 1 && st[0].Running
	}, "job running flag")
	close(unblock)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}

func TestHooksInvokedOnCompletion(t *testing.T) {
	var fired atomic.Int64
	hook := dcron.HookFunc(func(_ context.Context, _ dcron.Result) error {
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
	waitFor(t, func() bool { return fired.Load() >= 1 }, "hook to fire")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if fired.Load() == 0 {
		t.Fatal("hook was not invoked")
	}
}

func TestMetricsRecorderReceivesSignals(t *testing.T) {
	rec := newRecordingRecorder()
	s := testScheduler(newSchedBackend(), dcron.WithMetrics(rec))
	if err := s.Add("job", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.lastDone.Load() }, "job finished")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if rec.leaders.Load() == 0 {
		t.Fatalf("expected SetLeader(instance,true), saw %d", rec.leaders.Load())
	}
	if rec.started.Load() == 0 || rec.finished.Load() == 0 {
		t.Fatalf("expected JobStarted/JobFinished, got %d/%d", rec.started.Load(), rec.finished.Load())
	}
	if job, _ := rec.lastJob.Load().(string); job != "job" {
		t.Fatalf("metrics JobFinished job = %q; want job", job)
	}
}

func TestWebhookHookConstruction(t *testing.T) {
	headers := map[string]string{"X-Test": "123"}
	w := dcron.WebhookHook{
		URL:     "http://localhost:1/x",
		Timeout: 2 * time.Second,
		Headers: headers,
	}
	if w.URL != "http://localhost:1/x" {
		t.Fatalf("URL = %q", w.URL)
	}
	if w.Timeout != 2*time.Second {
		t.Fatalf("Timeout = %v; want 2s", w.Timeout)
	}
	if w.Headers["X-Test"] != "123" {
		t.Fatalf("Headers = %v", w.Headers)
	}

	wDefault := dcron.WebhookHook{URL: "http://127.0.0.1:0/invalid"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := wDefault.Fire(ctx, dcron.Result{Name: "job1", Outcome: dcron.OutcomeFailed})
	if err == nil {
		t.Fatal("Fire: expected error with unreachable URL")
	}
}

func TestWebhookHook_HTTPDelivery(t *testing.T) {
	var receivedMethod, receivedContentType, receivedAuthHeader string
	var receivedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedContentType = r.Header.Get("Content-Type")
		receivedAuthHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := &dcron.WebhookHook{
		URL:     srv.URL,
		Timeout: 2 * time.Second,
		Headers: map[string]string{"Authorization": "Bearer test-token"},
	}

	err := w.Fire(context.Background(), dcron.Result{
		Name:     "backup-job",
		Outcome:  dcron.OutcomeFailed,
		Attempts: 3,
		Duration: 1500 * time.Millisecond,
		Error:    errors.New("db connection reset"),
	})
	if err != nil {
		t.Fatalf("Fire: unexpected error: %v", err)
	}

	if receivedMethod != "POST" {
		t.Errorf("Method = %q; want POST", receivedMethod)
	}
	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", receivedContentType)
	}
	if receivedAuthHeader != "Bearer test-token" {
		t.Errorf("Authorization = %q; want Bearer test-token", receivedAuthHeader)
	}
	if receivedBody["job"] != "backup-job" {
		t.Errorf("body job = %v; want backup-job", receivedBody["job"])
	}
	if receivedBody["outcome"] != "failed" {
		t.Errorf("body outcome = %v; want failed", receivedBody["outcome"])
	}
	if receivedBody["error"] != "db connection reset" {
		t.Errorf("body error = %v; want db connection reset", receivedBody["error"])
	}

	// Test non-2xx status code handling
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()

	wFail := &dcron.WebhookHook{URL: errSrv.URL}
	if err := wFail.Fire(context.Background(), dcron.Result{Name: "job"}); err == nil {
		t.Fatal("Fire: expected error on 500 status code, got nil")
	}
}

func errIs(err, target error) bool { return errors.Is(err, target) }
func TestAddOnceRejectsPastTime(t *testing.T) {
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }
	past := time.Now().Add(-time.Minute)
	if err := s.AddOnce("expired", past, noop); !errIs(err, dcron.ErrInvalidSpec) {
		t.Fatalf("AddOnce with past time err = %v; want dcron.ErrInvalidSpec", err)
	}
}

func TestAddOnceDuplicateName(t *testing.T) {
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }
	at := time.Now().Add(time.Hour)
	if err := s.AddOnce("dup", at, noop); err != nil {
		t.Fatalf("AddOnce: %v", err)
	}
	if err := s.AddOnce("dup", at.Add(time.Hour), noop); !errIs(err, dcron.ErrJobExists) {
		t.Fatalf("duplicate AddOnce err = %v; want dcron.ErrJobExists", err)
	}
}

func TestRedactError(t *testing.T) {
	if got := dcron.RedactError(nil); got != "" {
		t.Fatalf("RedactError(nil) = %q; want empty string", got)
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean error unchanged",
			input:    "context deadline exceeded",
			expected: "context deadline exceeded",
		},
		{
			name:     "postgres url dsn password",
			input:    "dial tcp: lookup postgres://app_user:s3cr3t_pass@postgres.internal:5432/orders_db?sslmode=require: no such host",
			expected: "dial tcp: lookup postgres://app_user:[REDACTED]@postgres.internal:5432/orders_db?sslmode=require: no such host",
		},
		{
			name:     "bearer token in http error",
			input:    "request failed with header Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.xyz status 401",
			expected: "request failed with header Authorization: Bearer [REDACTED] status 401",
		},
		{
			name:     "key value credentials",
			input:    "auth failed: password=superSecretPass123! token=ghp_abcdef123456 api_key=sk_live_xyz789 user=bob",
			expected: "auth failed: password=[REDACTED] token=[REDACTED] api_key=[REDACTED] user=bob",
		},
		{
			name:     "multiline error with stack and dsn",
			input:    "job failed with:\nconnect error: postgresql://admin:p@ssword!@db:5432/prod\napi error: token: secret-token-42",
			expected: "job failed with:\nconnect error: postgresql://admin:[REDACTED]@db:5432/prod\napi error: token: [REDACTED]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dcron.RedactError(errors.New(tc.input))
			if got != tc.expected {
				t.Fatalf("RedactError:\n got:  %q\n want: %q", got, tc.expected)
			}
		})
	}
}

func TestWebhookHook_Redaction(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sensitiveErr := errors.New("failed connect to postgres://user:superSecretPassword@10.0.0.1:5432/db with token=secret123")

	t.Run("default redaction", func(t *testing.T) {
		w := &dcron.WebhookHook{
			URL:     srv.URL,
			Timeout: 2 * time.Second,
		}
		err := w.Fire(context.Background(), dcron.Result{
			Name:    "redacted-job",
			Outcome: dcron.OutcomeFailed,
			Error:   sensitiveErr,
		})
		if err != nil {
			t.Fatalf("Fire: %v", err)
		}
		errStr, _ := receivedBody["error"].(string)
		if strings.Contains(errStr, "superSecretPassword") || strings.Contains(errStr, "secret123") {
			t.Fatalf("leaked secret in error payload: %q", errStr)
		}
		if !strings.Contains(errStr, "[REDACTED]") {
			t.Fatalf("expected [REDACTED] in error payload: %q", errStr)
		}
	})

	t.Run("opt-in raw error", func(t *testing.T) {
		w := &dcron.WebhookHook{
			URL:             srv.URL,
			Timeout:         2 * time.Second,
			IncludeRawError: true,
		}
		err := w.Fire(context.Background(), dcron.Result{
			Name:    "raw-job",
			Outcome: dcron.OutcomeFailed,
			Error:   sensitiveErr,
		})
		if err != nil {
			t.Fatalf("Fire: %v", err)
		}
		errStr, _ := receivedBody["error"].(string)
		if errStr != sensitiveErr.Error() {
			t.Fatalf("got raw error %q; want %q", errStr, sensitiveErr.Error())
		}
	})

	t.Run("custom RedactFunc", func(t *testing.T) {
		w := &dcron.WebhookHook{
			URL:     srv.URL,
			Timeout: 2 * time.Second,
			RedactFunc: func(e error) string {
				return "CUSTOM_MASKED"
			},
		}
		err := w.Fire(context.Background(), dcron.Result{
			Name:    "custom-mask-job",
			Outcome: dcron.OutcomeFailed,
			Error:   sensitiveErr,
		})
		if err != nil {
			t.Fatalf("Fire: %v", err)
		}
		errStr, _ := receivedBody["error"].(string)
		if errStr != "CUSTOM_MASKED" {
			t.Fatalf("got error %q; want CUSTOM_MASKED", errStr)
		}
	})
}
