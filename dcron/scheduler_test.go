package dcron

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

	"github.com/mindfire-test/d-cron/internal/elector"
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

// fakeDriver/fakeConn let tests open a *sql.DB without a Postgres driver so
// the session-stability gate is testable in isolation.
type fakeDriver struct{}

func (fakeDriver) Open(_ string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("fake: no queries") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("fake: no tx") }

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
	s := newWithBackend(newSchedBackend(), nil, testCfg())
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
	s := newWithBackend(newSchedBackend(), nil, testCfg())
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
	s := newWithBackend(newSchedBackend(), nil, testCfg())
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
	s := newWithBackend(newSchedBackend(), nil, testCfg())
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

	if got := deriveIdempotencyKey("default", "report", fireAt); got != wantKey {
		t.Fatalf("deriveIdempotencyKey = %q; want %q", got, wantKey)
	}
	// Identical across replicas for the same fire time (issue #21) and across
	// different timezone representations of the same instant.
	ny := time.Date(2026, 8, 18, 22, 30, 0, 0, time.FixedZone("EDT", -4*3600))
	if got := deriveIdempotencyKey("default", "report", ny); got != wantKey {
		t.Fatalf("deriveIdempotencyKey must be invariant across timezones: %q != %q", got, wantKey)
	}
	if got := deriveIdempotencyKey("other", "report", fireAt); got == wantKey {
		t.Fatal("different namespaces must yield different keys")
	}
}

func TestNewSessionStabilityGate(t *testing.T) {
	// Register a minimal fake driver so sql.Open/db.Conn succeed; the gate is
	// then the only behavior under test, not driver plumbing.
	sql.Register("dcron-test-fake", fakeDriver{})
	db, err := sql.Open("dcron-test-fake", "unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, serr := New(db)
	if !errors.Is(serr, ErrSessionStabilityUnasserted) {
		t.Fatalf("New without assertion: err = %v; want ErrSessionStabilityUnasserted", serr)
	}
	var sse *SessionStabilityError
	if !errors.As(serr, &sse) {
		t.Fatalf("New without assertion: err = %T; want *SessionStabilityError", serr)
	}
	if _, err := New(db, WithSessionStableConnection(), WithLogger(discardLogger())); err != nil {
		t.Fatalf("New with WithSessionStableConnection must pass the gate, got %v", err)
	}
}

func TestSchedulerKeyExposed(t *testing.T) {
	cfg := testCfg()
	s := newWithBackend(newSchedBackend(), nil, cfg)
	if got, want := s.Key(), elector.LockKey(cfg.namespace); got != want {
		t.Fatalf("Key() = %d; want elector.LockKey(%q) = %d", got, cfg.namespace, want)
	}
}
