package elector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeBackend is a fully controllable Backend for state-machine tests. Every
// method honors the caller's context so the cancellation path is exercised
// without a real Postgres.
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
		return false, 0, nil // lock held by a different replica
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

func (f *fakeBackend) Release(ctx context.Context, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released++
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.relErr != nil {
		return f.relErr
	}
	f.held = false
	return nil
}

func (f *fakeBackend) Close() error { return nil }

func TestLockKeyDeterministic(t *testing.T) {
	a := LockKey("billing")
	if a == 0 {
		t.Fatalf("LockKey must never be zero")
	}
	if b := LockKey("billing"); b != a {
		t.Fatalf("LockKey not deterministic: %d != %d", b, a)
	}
	if c := LockKey("notifications"); c == a {
		t.Fatalf("different namespaces must yield different keys")
	}
}

func TestAcquirePromotesStandby(t *testing.T) {
	e := New("ns", newFakeBackend(), testLogger())
	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || !promoted || epoch != 1 {
		t.Fatalf("Acquire(_, _) = %v, %d, %v; want true, 1, nil", promoted, epoch, err)
	}
	if got := e.State(); got != StateLeader {
		t.Fatalf("State() = %s; want leader", got)
	}
	if e.Epoch() != 1 {
		t.Fatalf("Epoch() = %d; want 1", e.Epoch())
	}
}

func TestAcquireReconfirmsLeaderWithoutEpochBump(t *testing.T) {
	fb := newFakeBackend()
	e := New("ns", fb, testLogger())

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
	fb.held = false // conn no longer holds the lock (lost it)
	e := New("ns", fb, testLogger())
	e.state = StateLeader
	e.epoch = 1
	e.pid = 7

	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || promoted {
		t.Fatalf("Acquire after lost lock = %v, %d, %v; want false, 1, nil", promoted, epoch, err)
	}
	if got := e.State(); got != StateStandby {
		t.Fatalf("State() = %s; want standby", got)
	}
	if e.Epoch() != 1 {
		t.Fatalf("Epoch should be unchanged after losing leadership: got %d, want 1", e.Epoch())
	}
}

func TestAcquireWaitsForLockWhenHeldByOther(t *testing.T) {
	fb := &fakeBackend{held: true} // held by a different replica
	e := New("ns", fb, testLogger())
	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || promoted {
		t.Fatalf("Acquire while held by other = %v, %d, %v; want false, 0, nil", promoted, epoch, err)
	}
	if got := e.State(); got != StateStandby {
		t.Fatalf("State() = %s; want standby", got)
	}
}

func TestEpochMonotonicAcrossPromotions(t *testing.T) {
	fb := newFakeBackend()
	e := New("ns", fb, testLogger())
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
	e := New("ns", fb, testLogger())
	if err := e.Release(context.Background()); err != nil {
		t.Fatalf("Release as standby: %v", err)
	}
	if fb.released != 0 {
		t.Fatalf("backend.Release should not be called for a standby: got %d", fb.released)
	}
}

func TestReleaseReleasesOnlyAsLeader(t *testing.T) {
	fb := newFakeBackend()
	e := New("ns", fb, testLogger())
	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Release(context.Background()); err != nil {
		t.Fatalf("Release as leader: %v", err)
	}
	if fb.released != 1 {
		t.Fatalf("expected one backend.Release call, got %d", fb.released)
	}
	if got := e.State(); got != StateStandby {
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
		e := New("ns", fb, testLogger())
		_, _, err := e.Acquire(context.Background())
		if !errors.Is(err, fb.tryErr) {
			t.Fatalf("err = %v; want %v", err, fb.tryErr)
		}
	})
	t.Run("holds lock error", func(t *testing.T) {
		fb := newFakeBackend()
		fb.holdsErr = errors.New("probe failed")
		e := New("ns", fb, testLogger())
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
		e := New("ns", fb, testLogger())
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
		e := New("ns", fb, testLogger())
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
		e := New("ns", fb, testLogger())
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
	e := New("ns", fb, testLogger())
	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.PID() != 6 {
		t.Fatalf("PID() = %d; want 6", e.PID())
	}
}
