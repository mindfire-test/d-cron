package dcron_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/internal/elector"
)

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

func (b *schedBackend) Release(ctx context.Context, _ int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.held = false
	return true, nil
}

func (b *schedBackend) Close() error { return nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeDriver struct{}

func (fakeDriver) Open(_ string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("fake: no queries") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("fake: no tx") }

func testScheduler(backend dcron.LockBackend, opts ...dcron.Option) *dcron.Scheduler {
	base := []dcron.Option{
		dcron.WithPollInterval(5 * time.Millisecond),
		dcron.WithDrainTimeout(2 * time.Second),
		dcron.WithLogger(discardLogger()),
	}
	return dcron.NewWithBackend(backend, append(base, opts...)...)
}

func TestNewNilDB(t *testing.T) {
	if _, err := dcron.New(nil); !errors.Is(err, dcron.ErrNilDB) {
		t.Fatalf("dcron.New(nil) err = %v; want dcron.ErrNilDB", err)
	}
}

func TestAddValidation(t *testing.T) {
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }

	if err := s.Add("bad-spec", "not a spec", noop); !errors.Is(err, dcron.ErrInvalidSpec) {
		t.Fatalf("bad spec err = %v; want dcron.ErrInvalidSpec", err)
	}
	if err := s.Add("nil-fn", "*/5 * * * *", nil); !errors.Is(err, dcron.ErrNilJob) {
		t.Fatalf("nil fn err = %v; want dcron.ErrNilJob", err)
	}
	if err := s.Add("tick", "*/5 * * * *", noop); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add("tick", "*/5 * * * *", noop); !errors.Is(err, dcron.ErrJobExists) {
		t.Fatalf("duplicate err = %v; want dcron.ErrJobExists", err)
	}
}

func TestSchedulerLifecycle(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.Stop(context.Background()); !errors.Is(err, dcron.ErrNotStarted) {
		t.Fatalf("Stop before Start err = %v; want dcron.ErrNotStarted", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Start(context.Background()); !errors.Is(err, dcron.ErrAlreadyStarted) {
		t.Fatalf("second Start err = %v; want dcron.ErrAlreadyStarted", err)
	}
	if err := s.Add("late", "*/5 * * * *", func(context.Context) error { return nil }); !errors.Is(err, dcron.ErrAlreadyStarted) {
		t.Fatalf("Add after Start err = %v; want dcron.ErrAlreadyStarted", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := s.Stop(context.Background()); !errors.Is(err, dcron.ErrNotStarted) {
		t.Fatalf("Stop after Stop err = %v; want dcron.ErrNotStarted", err)
	}
}

func TestSchedulerFiresJobWhenLeader(t *testing.T) {
	s := testScheduler(newSchedBackend())
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
	s := testScheduler(newSchedBackend())
	var epochSeen atomic.Int64
	var keySeen atomic.Value
	jobName := "report"
	if err := s.Add(jobName, "@every 1ms", func(ctx context.Context) error {
		epochSeen.Store(dcron.Epoch(ctx))
		keySeen.Store(dcron.IdempotencyKey(ctx))
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
	key, _ := keySeen.Load().(string)
	if key == "" || key == jobName {
		t.Fatalf("idempotency key = %q; want a fire-time-derived key, not the job name", key)
	}
	if len(key) != 64 {
		t.Fatalf("idempotency key = %q; want a 64-char sha256 hex digest", key)
	}
}

func TestDeriveIdempotencyKey(t *testing.T) {
	fireAt := time.Date(2026, 8, 19, 2, 30, 0, 0, time.UTC)
	want := fmt.Sprintf("d-cron:v1:default:report:%s", fireAt.UTC().Format(time.RFC3339))
	sum := sha256.Sum256([]byte(want))
	wantKey := hex.EncodeToString(sum[:])

	if got := dcron.DeriveIdempotencyKey("default", "report", fireAt); got != wantKey {
		t.Fatalf("deriveIdempotencyKey = %q; want %q", got, wantKey)
	}

	ny := time.Date(2026, 8, 18, 22, 30, 0, 0, time.FixedZone("EDT", -4*3600))
	if got := dcron.DeriveIdempotencyKey("default", "report", ny); got != wantKey {
		t.Fatalf("deriveIdempotencyKey must be invariant across timezones: %q != %q", got, wantKey)
	}
	if got := dcron.DeriveIdempotencyKey("other", "report", fireAt); got == wantKey {
		t.Fatal("different namespaces must yield different keys")
	}
}

func TestNewSessionStabilityGate(t *testing.T) {
	sql.Register("dcron-test-fake", fakeDriver{})
	db, err := sql.Open("dcron-test-fake", "unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, serr := dcron.New(db)
	if !errors.Is(serr, dcron.ErrSessionStabilityUnasserted) {
		t.Fatalf("New without assertion: err = %v; want dcron.ErrSessionStabilityUnasserted", serr)
	}
	var sse *dcron.SessionStabilityError
	if !errors.As(serr, &sse) {
		t.Fatalf("New without assertion: err = %T; want *dcron.SessionStabilityError", serr)
	}
	if _, err := dcron.New(db, dcron.WithSessionStableConnection(), dcron.WithLogger(discardLogger())); err != nil {
		t.Fatalf("New with WithSessionStableConnection must pass the gate, got %v", err)
	}
}

func TestSchedulerKeyExposed(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if got, want := s.Key(), elector.LockKey("default"); got != want {
		t.Fatalf("Key() = %d; want elector.LockKey(default) = %d", got, want)
	}
}

func TestSchedulerStopDrainTimeout(t *testing.T) {
	s := testScheduler(newSchedBackend(), dcron.WithDrainTimeout(50*time.Millisecond))
	started := make(chan struct{})
	if err := s.Add("stuck", "@every 1ms", func(ctx context.Context) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		time.Sleep(1 * time.Second) // stuck job
		return ctx.Err()
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started

	stopStart := time.Now()
	err := s.Stop(context.Background())
	dur := time.Since(stopStart)

	if err == nil {
		t.Fatalf("Stop err = nil; want drain timeout error context.DeadlineExceeded")
	}
	if dur > 500*time.Millisecond {
		t.Fatalf("Stop took %v; want bounded by drain timeout (~50ms)", dur)
	}
}

func TestSchedulerNoOverlapWithRetry(t *testing.T) {
	s := testScheduler(newSchedBackend())
	var active atomic.Int32
	var maxActive atomic.Int32
	var attempts atomic.Int32
	successCh := make(chan struct{})

	err := s.Add("retry-no-overlap", "@every 5ms", func(ctx context.Context) error {
		cur := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxActive.Load()
			if cur <= old || maxActive.CompareAndSwap(old, cur) {
				break
			}
		}

		att := attempts.Add(1)
		if att < 3 {
			// Fail first two attempts to trigger retry backoff
			return errors.New("transient error")
		}
		select {
		case <-successCh:
		default:
			close(successCh)
		}
		return nil
	}, dcron.WithNoOverlap(), dcron.WithRetry(dcron.Retry{
		Attempts: 3,
		Backoff:  15 * time.Millisecond,
	}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()

	select {
	case <-successCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job to succeed across retries")
	}

	if max := maxActive.Load(); max > 1 {
		t.Fatalf("overlapping executions detected: max active = %d, want <= 1", max)
	}
	if att := attempts.Load(); att != 3 {
		t.Fatalf("attempts = %d, want 3", att)
	}
}

func TestSchedulerRetryBackoffCancellation(t *testing.T) {
	backend := &controllableBackend{}
	completedCh := make(chan dcron.Result, 10)
	hook := dcron.HookFunc(func(ctx context.Context, res dcron.Result) error {
		if res.Name == "cancel-during-backoff" {
			completedCh <- res
		}
		return nil
	})

	s := testScheduler(backend, dcron.WithPollInterval(5*time.Millisecond), dcron.WithHooks(hook))

	var attempts atomic.Int32
	inBackoff := make(chan struct{})
	run2Started := make(chan struct{})

	err := s.Add("cancel-during-backoff", "@every 5ms", func(ctx context.Context) error {
		att := attempts.Add(1)
		if att == 1 {
			// When runAttempt finishes, its attemptCtx is cancelled by executor.Run before entering sleep.
			// Waiting on ctx.Done() in a goroutine signals deterministically that attempt 1 has finished
			// and the job has entered retry backoff.
			go func(attemptCtx context.Context) {
				<-attemptCtx.Done()
				close(inBackoff)
			}(ctx)
			return errors.New("transient failure entering retry backoff")
		}
		// Subsequent execution after demotion and re-promotion
		select {
		case <-run2Started:
		default:
			close(run2Started)
		}
		return nil
	}, dcron.WithNoOverlap(), dcron.WithRetry(dcron.Retry{
		Attempts: 5,
		Backoff:  10 * time.Second, // Long backoff ensures failure if cancellation does not promptly interrupt sleep
	}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()

	// 1. Wait deterministically for attempt 1 to finish and enter retry backoff
	select {
	case <-inBackoff:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for attempt 1 to enter retry backoff")
	}

	// 2. Cancel leadership context while the job is blocked in retry backoff
	startCancel := time.Now()
	backend.LoseLock()

	// 3. Verify execution stops promptly with OutcomeCanceled
	select {
	case res := <-completedCh:
		dur := time.Since(startCancel)
		if dur > 2*time.Second {
			t.Fatalf("backoff cancellation took %v; want prompt stop (<2s)", dur)
		}
		if res.Outcome != dcron.OutcomeCanceled {
			t.Fatalf("outcome = %v; want %v", res.Outcome, dcron.OutcomeCanceled)
		}
		if res.Attempts != 1 {
			t.Fatalf("attempts in result = %d; want 1", res.Attempts)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job to cancel during retry backoff")
	}

	// 4. Verify no additional retry attempt was started
	if att := attempts.Load(); att != 1 {
		t.Fatalf("attempts = %d; want exactly 1 attempt before cancellation", att)
	}

	// 5. Restore leadership and verify the job mutex was released correctly
	// If j.busy was left locked, the subsequent scheduled tick could not acquire the lock.
	backend.RegainLock()

	select {
	case <-run2Started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subsequent run; job mutex was not released correctly")
	}

	if att := attempts.Load(); att != 2 {
		t.Fatalf("attempts = %d; want 2 after re-promotion execution", att)
	}
}

func TestSchedulerQueuedRunConcurrencyRace(t *testing.T) {
	s := testScheduler(newSchedBackend(), dcron.WithPollInterval(2*time.Millisecond))
	var active atomic.Int32
	var maxActive atomic.Int32
	var completed atomic.Int32

	err := s.Add("queued-race", "@every 5ms", func(ctx context.Context) error {
		cur := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxActive.Load()
			if cur <= old || maxActive.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond) // slow enough that multiple ticks queue
		completed.Add(1)
		return nil
	}, dcron.WithOverlapPolicy(dcron.OverlapQueue))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for completed.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if comp := completed.Load(); comp < 3 {
		t.Fatalf("completed runs = %d, want >= 3", comp)
	}
	if max := maxActive.Load(); max > 1 {
		t.Fatalf("max active executions with OverlapQueue = %d, want <= 1", max)
	}
}

type controllableBackend struct {
	mu        sync.Mutex
	held      bool
	lockedOut bool
}

func (b *controllableBackend) TryLock(ctx context.Context, _ int64) (bool, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lockedOut || b.held {
		return false, 0, nil
	}
	b.held = true
	return true, 1, nil
}

func (b *controllableBackend) HoldsLock(ctx context.Context, _ int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lockedOut {
		return false, nil
	}
	return b.held, nil
}

func (b *controllableBackend) Release(ctx context.Context, _ int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held = false
	return true, nil
}

func (b *controllableBackend) Close() error { return nil }

func (b *controllableBackend) LoseLock() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held = false
	b.lockedOut = true
}

func (b *controllableBackend) RegainLock() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lockedOut = false
}

func TestSchedulerQueuedJobLeadershipTransition(t *testing.T) {
	backend := &controllableBackend{}
	s := testScheduler(backend, dcron.WithPollInterval(5*time.Millisecond))

	firstRunStarted := make(chan struct{})
	allowFirstRunFinish := make(chan struct{})
	var queuedRunExecuted atomic.Bool
	var repromotedRunExecuted atomic.Bool
	var repromotedEpoch atomic.Int64

	err := s.Add("transition-job", "@every 5ms", func(ctx context.Context) error {
		select {
		case <-firstRunStarted:
			if backend.lockedOut {
				// Queued run executed while demoted!
				queuedRunExecuted.Store(true)
				return nil
			}
			repromotedRunExecuted.Store(true)
			repromotedEpoch.Store(dcron.Epoch(ctx))
			return nil
		default:
			// First run
			close(firstRunStarted)
			<-allowFirstRunFinish
			return nil
		}
	}, dcron.WithOverlapPolicy(dcron.OverlapQueue))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()

	// Wait until first run starts
	<-firstRunStarted

	// Allow multiple ticks to queue
	time.Sleep(20 * time.Millisecond)

	// Induce demotion by releasing lock behind the leader and locking out
	backend.LoseLock()

	// Wait for standby poll to detect lost lock and demote
	time.Sleep(25 * time.Millisecond)

	// Allow first run to finish
	close(allowFirstRunFinish)

	// Wait to verify queued run is NOT executed while demoted
	time.Sleep(60 * time.Millisecond)

	if queuedRunExecuted.Load() {
		t.Fatal("queued run was dispatched after leadership demotion; want skipped")
	}

	// Now regain lock to trigger re-promotion
	backend.RegainLock()

	// Wait for re-promotion and subsequent scheduled run
	deadline := time.Now().Add(2 * time.Second)
	for !repromotedRunExecuted.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if !repromotedRunExecuted.Load() {
		t.Fatal("expected new execution after re-promotion, got none")
	}
	if ep := repromotedEpoch.Load(); ep <= 1 {
		t.Fatalf("expected re-promoted epoch to be incremented (>1), got %d", ep)
	}
}

func TestSchedulerStartupTimeout(t *testing.T) {
	const driverName = "dcron-startup-timeout-driver"
	sql.Register(driverName, fakeDriver{})
	db, err := sql.Open(driverName, "unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	start := time.Now()
	_, err = dcron.New(db,
		dcron.WithSessionStableConnection(),
		dcron.WithStartupTimeout(25*time.Millisecond),
		dcron.WithDedicatedLockConn(func(ctx context.Context) (*sql.Conn, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}),
		dcron.WithLogger(discardLogger()),
	)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded on startup timeout, got %v", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("startup took too long to abort on timeout: %v", elapsed)
	}
}

func TestSchedulerTimeoutOptions(t *testing.T) {
	const driverName = "dcron-timeout-opts-driver"
	sql.Register(driverName, fakeDriver{})
	db, err := sql.Open(driverName, "unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s, err := dcron.New(db,
		dcron.WithSessionStableConnection(),
		dcron.WithStartupTimeout(500*time.Millisecond),
		dcron.WithPollTimeout(200*time.Millisecond),
		dcron.WithLogger(discardLogger()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil scheduler")
	}
}
