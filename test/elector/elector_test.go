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
	holdsFn  func(ctx context.Context, key int64) (bool, error)
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

func (f *fakeBackend) HoldsLock(ctx context.Context, key int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds++
	if f.holdsFn != nil {
		return f.holdsFn(ctx, key)
	}
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

func TestAcquireTransientProbeTimeoutRetriesAndMaintainsLeadership(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())
	// Acquire leadership initially
	promoted, epoch, err := e.Acquire(context.Background())
	if err != nil || !promoted || epoch != 1 {
		t.Fatalf("first Acquire promoted = %v, epoch = %d, err = %v", promoted, epoch, err)
	}

	transitions := e.Subscribe()

	// Simulate transient timeout on attempt 1, success on attempt 2
	var attempts int
	fb.holdsFn = func(ctx context.Context, key int64) (bool, error) {
		attempts++
		if attempts == 1 {
			return false, context.DeadlineExceeded
		}
		return true, nil
	}

	promoted, epoch, err = e.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire failed on transient error: %v", err)
	}
	if promoted {
		t.Fatal("Acquire reported promoted when already leader")
	}
	if epoch != 1 {
		t.Fatalf("epoch changed on transient probe failure: got %d, want 1", epoch)
	}
	if !e.IsLeader() {
		t.Fatal("elector lost leadership on transient probe timeout")
	}
	if attempts != 2 {
		t.Fatalf("expected 2 probe attempts, got %d", attempts)
	}

	select {
	case tr := <-transitions:
		t.Fatalf("unexpected transition emitted: %+v", tr)
	default:
	}
}

func TestAcquirePersistentProbeTimeoutDemotes(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())
	// Acquire leadership initially
	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	transitions := e.Subscribe()

	// Fail all probe attempts with timeout
	var attempts int
	fb.holdsFn = func(ctx context.Context, key int64) (bool, error) {
		attempts++
		return false, context.DeadlineExceeded
	}

	promoted, epoch, err := e.Acquire(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if promoted {
		t.Fatal("Acquire reported promoted")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 probe attempts before demoting, got %d", attempts)
	}
	if e.IsLeader() {
		t.Fatal("elector remained leader after persistent timeouts")
	}
	if got := e.State(); got != elector.StateDemoting {
		t.Fatalf("expected StateDemoting, got %s", got)
	}
	if epoch != 2 {
		t.Fatalf("expected epoch 2 after demotion, got %d", epoch)
	}

	select {
	case tr := <-transitions:
		if tr.From != elector.StateLeader || tr.To != elector.StateDemoting || tr.Reason != "db_unavailable" {
			t.Fatalf("unexpected transition: %+v", tr)
		}
	default:
		t.Fatal("expected transition event for demotion, got none")
	}
}

func TestAcquireProbeHonorsContextCancellationDuringRetry(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())
	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	fb.holdsFn = func(c context.Context, key int64) (bool, error) {
		cancel()
		return false, context.DeadlineExceeded
	}

	_, _, err := e.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if e.State() == elector.StateDemoting {
		t.Fatal("elector transitioned to StateDemoting on parent context cancellation")
	}
}

func TestSetProbeTimeoutEnforced(t *testing.T) {
	fb := newFakeBackend()
	e := elector.New("ns", "test", fb, testLogger())
	e.SetProbeTimeout(100 * time.Millisecond)

	var probeDeadline time.Time
	var hasDeadline bool
	fb.holdsFn = func(ctx context.Context, key int64) (bool, error) {
		probeDeadline, hasDeadline = ctx.Deadline()
		return true, nil
	}

	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline {
		t.Fatal("probe context had no deadline")
	}
	remaining := time.Until(probeDeadline)
	if remaining <= 0 || remaining > 100*time.Millisecond {
		t.Fatalf("probe deadline remaining = %v; expected <= 100ms", remaining)
	}
}
