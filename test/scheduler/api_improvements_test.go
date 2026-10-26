package dcron_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/otel"
)

// 1. Point 3: Registration Consistency (Add vs AddDynamic)
func TestRegistrationConsistency_SinceSuccessRequiresHistory(t *testing.T) {
	s := testScheduler(newSchedBackend())

	// Add should reject WithSinceLastSuccess without history
	errAdd := s.Add("since1", "@every 1s", func(_ context.Context) error { return nil },
		dcron.WithSinceLastSuccess(time.Second))
	if errAdd == nil {
		t.Fatal("Add: expected error when registering WithSinceLastSuccess without history")
	}

	// AddDynamic should also reject WithSinceLastSuccess without history
	errDyn := s.AddDynamic("since2", "@every 1s", func(_ context.Context) error { return nil },
		dcron.WithSinceLastSuccess(time.Second))
	if errDyn == nil {
		t.Fatal("AddDynamic: expected error when registering WithSinceLastSuccess without history")
	}

	// Both should reject MissedCatchUp without history
	errAddCatch := s.Add("catch1", "@every 1s", func(_ context.Context) error { return nil },
		dcron.WithMissedRunPolicy(dcron.MissedCatchUp))
	if errAddCatch == nil {
		t.Fatal("Add: expected error when registering MissedCatchUp without history")
	}

	errDynCatch := s.AddDynamic("catch2", "@every 1s", func(_ context.Context) error { return nil },
		dcron.WithMissedRunPolicy(dcron.MissedCatchUp))
	if errDynCatch == nil {
		t.Fatal("AddDynamic: expected error when registering MissedCatchUp without history")
	}
}

// 2. Point 2: Retry API Semantics
func TestRetryAPI_WithNoRetry(t *testing.T) {
	var attempts atomic.Int64
	var completedCh = make(chan dcron.Result, 1)

	s := testScheduler(newSchedBackend(), dcron.WithHooks(dcron.HookFunc(func(_ context.Context, res dcron.Result) error {
		completedCh <- res
		return nil
	})))

	err := s.Add("fail_no_retry", "@every 10ms", func(_ context.Context) error {
		attempts.Add(1)
		return errors.New("deliberate boom")
	}, dcron.WithNoRetry())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case res := <-completedCh:
		if res.Attempts != 1 {
			t.Fatalf("attempts = %d; want exactly 1 for WithNoRetry", res.Attempts)
		}
		if res.Outcome != dcron.OutcomeFailed {
			t.Fatalf("outcome = %v; want failed", res.Outcome)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job completion")
	}

	if att := attempts.Load(); att != 1 {
		t.Fatalf("job executed %d times; want exactly 1", att)
	}
}

func TestRetryAPI_RetryableFilter(t *testing.T) {
	errFatal := errors.New("fatal domain error")
	var attempts atomic.Int64
	var completedCh = make(chan dcron.Result, 1)

	s := testScheduler(newSchedBackend(), dcron.WithHooks(dcron.HookFunc(func(_ context.Context, res dcron.Result) error {
		completedCh <- res
		return nil
	})))

	err := s.Add("filtered_retry", "@every 10ms", func(_ context.Context) error {
		attempts.Add(1)
		return errFatal
	}, dcron.WithRetry(dcron.Retry{
		Attempts: 5,
		Retryable: func(err error) bool {
			return !errors.Is(err, errFatal)
		},
	}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case res := <-completedCh:
		if res.Attempts != 1 {
			t.Fatalf("attempts = %d; want 1 because error is not retryable", res.Attempts)
		}
		if res.Outcome != dcron.OutcomeFailed {
			t.Fatalf("outcome = %v; want failed", res.Outcome)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job completion")
	}

	if att := attempts.Load(); att != 1 {
		t.Fatalf("job executed %d times; want exactly 1 due to non-retryable error", att)
	}
}

// 3. Point 1: Tracing with WithTracer
type testSpanRecord struct {
	JobName     string
	ScheduledAt time.Time
	Attempt     int
	Epoch       int64
	Outcome     string
	Err         error
}

type recordingTracer struct {
	mu      sync.Mutex
	records []testSpanRecord
}

var _ otel.Tracer = (*recordingTracer)(nil)

func (r *recordingTracer) StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(string, error)) {
	rec := testSpanRecord{
		JobName:     jobName,
		ScheduledAt: scheduledAt,
		Attempt:     attempt,
		Epoch:       epoch,
	}
	return ctx, func(outcome string, err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		rec.Outcome = outcome
		rec.Err = err
		r.records = append(r.records, rec)
	}
}

func TestScheduler_WithTracer(t *testing.T) {
	tracer := &recordingTracer{}
	s := testScheduler(newSchedBackend(), dcron.WithTracer(tracer))

	executed := make(chan struct{})
	err := s.Add("traced_job", "@every 10ms", func(_ context.Context) error {
		close(executed)
		return nil
	}, dcron.WithNoRetry())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-executed:
	case <-time.After(3 * time.Second):
		t.Fatal("job did not execute")
	}

	// Allow span end callback to complete
	time.Sleep(50 * time.Millisecond)

	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	if len(tracer.records) == 0 {
		t.Fatal("expected at least 1 span recorded by tracer")
	}
	span := tracer.records[0]
	if span.JobName != "traced_job" {
		t.Errorf("span.JobName = %q; want traced_job", span.JobName)
	}
	if span.Outcome != dcron.OutcomeOK {
		t.Errorf("span.Outcome = %q; want ok", span.Outcome)
	}
}

// 4. Points 4 & 5: Admin Mutations Rejected on Standby with ErrNotLeader and 409 Conflict
type standbyBackend struct{}

func (standbyBackend) TryLock(ctx context.Context, _ int64) (bool, int, error) { return false, 0, nil }
func (standbyBackend) HoldsLock(ctx context.Context, _ int64) (bool, error)    { return false, nil }
func (standbyBackend) Release(ctx context.Context, _ int64) (bool, error)      { return false, nil }
func (standbyBackend) Close() error                                            { return nil }

func TestStandbyMutations_RejectWithErrNotLeader(t *testing.T) {
	s := testScheduler(standbyBackend{})
	if err := s.Add("standby_job", "@every 1m", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify scheduler enters Standby mode
	waitFor(func() bool { return s.Leadership() == dcron.LeadershipStandby }, "standby state")

	// Verify Pause returns ErrNotLeader
	if err := s.Pause("standby_job"); !errors.Is(err, dcron.ErrNotLeader) {
		t.Errorf("Pause on standby: got %v; want ErrNotLeader", err)
	}

	// Verify Resume returns ErrNotLeader
	if err := s.Resume("standby_job"); !errors.Is(err, dcron.ErrNotLeader) {
		t.Errorf("Resume on standby: got %v; want ErrNotLeader", err)
	}

	// Verify Remove returns ErrNotLeader
	if err := s.Remove("standby_job"); !errors.Is(err, dcron.ErrNotLeader) {
		t.Errorf("Remove on standby: got %v; want ErrNotLeader", err)
	}

	// Verify AddDynamic returns ErrNotLeader
	if err := s.AddDynamic("dynamic_job", "@every 1m", func(_ context.Context) error { return nil }); !errors.Is(err, dcron.ErrNotLeader) {
		t.Errorf("AddDynamic on standby: got %v; want ErrNotLeader", err)
	}

	// Test AdminHandler endpoints against standby replica
	h := dcron.AdminHandler(nil, s)

	// POST /pause -> 409 Conflict
	{
		req := httptest.NewRequest(http.MethodPost, "/jobs/standby_job/pause", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusConflict {
			t.Errorf("Admin POST /pause on standby: got %d, want %d", rr.Code, http.StatusConflict)
		}
	}

	// POST /resume -> 409 Conflict
	{
		req := httptest.NewRequest(http.MethodPost, "/jobs/standby_job/resume", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusConflict {
			t.Errorf("Admin POST /resume on standby: got %d, want %d", rr.Code, http.StatusConflict)
		}
	}

	// DELETE -> 409 Conflict
	{
		req := httptest.NewRequest(http.MethodDelete, "/jobs/standby_job", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusConflict {
			t.Errorf("Admin DELETE on standby: got %d, want %d", rr.Code, http.StatusConflict)
		}
	}
}
