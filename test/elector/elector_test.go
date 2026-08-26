package elector_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/internal/elector"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeBackend struct {
	mu       sync.Mutex
	held     bool
	pid      int
	nextPID  int
	tryErr   error
	holdsErr error
	relErr   error
	released int
	tries    int
	holds    int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{nextPID: 1}
}

func (f *fakeBackend) TryLock(ctx context.Context, _ int64) (bool, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tries++
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	if f.tryErr != nil {
		return false, 0, f.tryErr
	}
	if f.held {
		return false, 0, nil
	}
	f.held = true
	f.pid = f.nextPID
	f.nextPID++
	return true, f.pid, nil
}

func (f *fakeBackend) HoldsLock(ctx context.Context, _ int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.holdsErr != nil {
		return false, f.holdsErr
	}
	return f.held, nil
}

func (f *fakeBackend) Release(ctx context.Context, _ int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.relErr != nil {
		return false, f.relErr
	}
	f.held = false
	return true, nil
}

func (f *fakeBackend) Close() error { return nil }

func TestLockKeyDeterministic(t *testing.T) {
	a := elector.LockKey("billing")
	if a == 0 {
		t.Fatalf("elector.LockKey must never be zero")
	}
	if b := elector.LockKey("billing"); b != a {
		t.Fatalf("elector.LockKey not deterministic: %d != %d", b, a)
	}
	if c := elector.LockKey("notifications"); c == a {
		t.Fatalf("different namespaces must yield different keys")
	}
}

func TestLockKeyEmptyAndUnicode(t *testing.T) {
	for _, ns := range []string{"", "default", "ümlaut-namespace", "🎉", "\x00control"} {
		if got := elector.LockKey(ns); got == 0 {
			t.Errorf("elector.LockKey(%q) = 0; must never be zero", ns)
		}
		if again := elector.LockKey(ns); again != elector.LockKey(ns) {
			t.Errorf("elector.LockKey(%q) not deterministic", ns)
		}
	}
	if elector.LockKey("") == elector.LockKey("default") {
		t.Fatal("empty namespace must not collide with the default namespace")
	}
}

func TestAcquirePromotesStandby(t *testing.T) {
	e := elector.New("ns", "test", newFakeBackend(), testLogger())
	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || !promoted || epoch != 1 {
		t.Fatalf("Acquire(_, _) = %v, %d, %v; want true, 1, nil", promoted, epoch, err)
	}
	if got := e.State(); got != elector.StateLeader {
		t.Fatalf("State() = %s; want leader", got)
	}
	if e.Epoch() != 1 {
		t.Fatalf("Epoch() = %d; want 1", e.Epoch())
	}
}

func TestAcquireReconfirmsLeaderWithoutEpochBump(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())

	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || promoted {
		t.Fatalf("re-confirm = %v, %d, %v; want false, same epoch, nil", promoted, epoch, err)
	}
	if fb.holds != 1 {
		t.Fatalf("expected exactly one HoldsLock probe, got %d", fb.holds)
	}
	if e.Epoch() != 1 {
		t.Fatalf("Epoch bumped on re-confirm: got %d, want 1", e.Epoch())
	}
}

func TestAcquireDemotesOnLostLock(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())

	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	fb.held = false
	sub := e.Subscribe()

	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || promoted {
		t.Fatalf("Acquire after lost lock = %v, %d, %v; want false, bumped epoch, nil", promoted, epoch, err)
	}
	if got := e.State(); got != elector.StateDemoting {
		t.Fatalf("State() = %s; want demoting (LEADER -> DEMOTING)", got)
	}
	if e.Epoch() != 2 {
		t.Fatalf("Epoch should be bumped on demotion (fence): got %d, want 2", e.Epoch())
	}
	e.FinalizeDemotion()
	if got := e.State(); got != elector.StateStandby {
		t.Fatalf("State() after FinalizeDemotion = %s; want standby", got)
	}

	var to []elector.State
	for len(to) < 2 {
		select {
		case t := <-sub:
			to = append(to, t.To)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for transition events")
		}
	}
	if to[0] != elector.StateDemoting || to[1] != elector.StateStandby {
		t.Fatalf("transition targets = %v; want [demoting standby]", to)
	}
}

func TestAcquireWaitsForLockWhenHeldByOther(t *testing.T) {
	fb := &fakeBackend{held: true}
	e := elector.New("ns", "test", fb, testLogger())
	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || promoted {
		t.Fatalf("Acquire while held by other = %v, %d, %v; want false, 0, nil", promoted, epoch, err)
	}
	if got := e.State(); got != elector.StateStandby {
		t.Fatalf("State() = %s; want standby", got)
	}
}

func TestEpochMonotonicAcrossPromotions(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())
	_, e1, err := e.Acquire(context.Background())
	if err != nil || e1 != 1 {
		t.Fatalf("first Acquire epoch = %d, err %v; want 1", e1, err)
	}
	if err := e.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, e2, err := e.Acquire(context.Background())
	if err != nil || e2 != 2 {
		t.Fatalf("second Acquire epoch = %d, err %v; want 2", e2, err)
	}
}

func TestReleaseNoopAsStandby(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())
	if err := e.Release(context.Background()); err != nil {
		t.Fatalf("Release as standby: %v", err)
	}
	if fb.released != 0 {
		t.Fatalf("backend.Release should not be called for a standby: got %d", fb.released)
	}
}

func TestReleaseReleasesOnlyAsLeader(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())
	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Release(context.Background()); err != nil {
		t.Fatalf("Release as leader: %v", err)
	}
	if fb.released != 1 {
		t.Fatalf("expected one backend.Release call, got %d", fb.released)
	}
	if got := e.State(); got != elector.StateStandby {
		t.Fatalf("State() = %s after Release; want standby", got)
	}
	if e.IsLeader() {
		t.Fatal("IsLeader() should be false after Release")
	}
}

func TestAcquirePropagatesBackendErrors(t *testing.T) {
	t.Run("try lock error", func(t *testing.T) {
		fb := newFakeBackend()
		fb.tryErr = errors.New("connection reset")
		e := elector.New("ns", "test", fb, testLogger())
		_, _, err := e.Acquire(context.Background())
		if !errors.Is(err, fb.tryErr) {
			t.Fatalf("err = %v; want %v", err, fb.tryErr)
		}
	})
	t.Run("holds lock error", func(t *testing.T) {
		fb := newFakeBackend()
		fb.holdsErr = errors.New("probe failed")
		e := elector.New("ns", "test", fb, testLogger())
		if _, _, err := e.Acquire(context.Background()); err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		_, _, err := e.Acquire(context.Background())
		if !errors.Is(err, fb.holdsErr) {
			t.Fatalf("err = %v; want %v", err, fb.holdsErr)
		}
	})
	t.Run("release error", func(t *testing.T) {
		fb := newFakeBackend()
		fb.relErr = errors.New("unlock failed")
		e := elector.New("ns", "test", fb, testLogger())
		if _, _, err := e.Acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e.Release(context.Background()); !errors.Is(err, fb.relErr) {
			t.Fatalf("err = %v; want %v", err, fb.relErr)
		}
	})
}

func TestAcquireHonorsContextCancellation(t *testing.T) {
	t.Run("leader re-confirm", func(t *testing.T) {
		fb := newFakeBackend()
		e := elector.New("ns", "test", fb, testLogger())
		ctx, cancel := context.WithCancel(context.Background())
		if _, _, err := e.Acquire(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		_, _, err := e.Acquire(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want context.Canceled", err)
		}
	})
	t.Run("standby try lock", func(t *testing.T) {
		fb := newFakeBackend()
		e := elector.New("ns", "test", fb, testLogger())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := e.Acquire(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want context.Canceled", err)
		}
	})
}

func TestPIDTracksCurrentHolder(t *testing.T) {
	fb := &fakeBackend{nextPID: 6}
	e := elector.New("ns", "test", fb, testLogger())
	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.PID() != 6 {
		t.Fatalf("PID() = %d; want 6", e.PID())
	}
}
