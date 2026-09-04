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

func testScheduler(backend *schedBackend, opts ...dcron.Option) *dcron.Scheduler {
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

