package dcron_test

// Tests for runtime job management: Pause, Resume, Remove, AddDynamic
// (issue #50, FR-211).
//
// These tests use the in-process fakeBackend so they run without Postgres.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
)

// newManagedScheduler returns a started scheduler with an always-leader fake
// backend, an @every 20ms job called "tick", and a cancel func to stop it.
func newManagedScheduler(t *testing.T) (*dcron.Scheduler, context.CancelFunc) {
	t.Helper()
	s := testScheduler(newSchedBackend())
	if err := s.Add("tick", "@every 50ms", func(_ context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s, cancel
}

// --------------------------------------------------------------------------
// Pause / Resume
// --------------------------------------------------------------------------

func TestPause_PreventsDispatch(t *testing.T) {
	var fired atomic.Int64
	s := testScheduler(newSchedBackend())
	if err := s.Add("counting", "@every 30ms", func(_ context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Let it fire at least once.
	time.Sleep(80 * time.Millisecond)

	// Pause and record the count.
	if err := s.Pause("counting"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	countAtPause := fired.Load()

	// Wait two more fire windows — counter must not advance.
	time.Sleep(80 * time.Millisecond)
	if fired.Load() != countAtPause {
		t.Errorf("Pause: job fired %d time(s) after pausing; want 0",
			fired.Load()-countAtPause)
	}
}

func TestResume_ReenablesDispatch(t *testing.T) {
	var fired atomic.Int64
	s := testScheduler(newSchedBackend())
	if err := s.Add("res", "@every 30ms", func(_ context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Pause immediately.
	if err := s.Pause("res"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	countAfterPause := fired.Load()

	// Resume — job must fire again.
	if err := s.Resume("res"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if fired.Load() <= countAfterPause {
		t.Errorf("Resume: no new fires after resume; before=%d after=%d",
			countAfterPause, fired.Load())
	}
}

func TestPause_UnknownJob(t *testing.T) {
	s, cancel := newManagedScheduler(t)
	defer cancel()
	err := s.Pause("no-such-job")
	if !errors.Is(err, dcron.ErrJobNotFound) {
		t.Errorf("Pause: got %v; want ErrJobNotFound", err)
	}
}

func TestResume_UnknownJob(t *testing.T) {
	s, cancel := newManagedScheduler(t)
	defer cancel()
	err := s.Resume("no-such-job")
	if !errors.Is(err, dcron.ErrJobNotFound) {
		t.Errorf("Resume: got %v; want ErrJobNotFound", err)
	}
}

// --------------------------------------------------------------------------
// Remove
// --------------------------------------------------------------------------

func TestRemove_StopsFiringAndFreesName(t *testing.T) {
	var fired atomic.Int64
	s := testScheduler(newSchedBackend())
	if err := s.Add("rm-job", "@every 30ms", func(_ context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(80 * time.Millisecond)
	if err := s.Remove("rm-job"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	at := fired.Load()

	// The clock still fires the already-enqueued heap entry but the jobs map
	// lookup returns nil and the entry is silently skipped.
	time.Sleep(80 * time.Millisecond)
	if fired.Load() > at+1 {
		// Allow at most 1 extra fire from a race between removal and dispatch.
		t.Errorf("Remove: job fired %d extra time(s) after removal",
			fired.Load()-at)
	}
}

func TestRemove_NameCanBeReregistered(t *testing.T) {
	s, cancel := newManagedScheduler(t)
	defer cancel()
	if err := s.Remove("tick"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// After removal the name is free — AddDynamic should succeed.
	if err := s.AddDynamic("tick", "@every 1m", func(_ context.Context) error { return nil }); err != nil {
		t.Errorf("AddDynamic after Remove: %v", err)
	}
}

func TestRemove_UnknownJob(t *testing.T) {
	s, cancel := newManagedScheduler(t)
	defer cancel()
	err := s.Remove("ghost")
	if !errors.Is(err, dcron.ErrJobNotFound) {
		t.Errorf("Remove: got %v; want ErrJobNotFound", err)
	}
}

// --------------------------------------------------------------------------
// AddDynamic
// --------------------------------------------------------------------------

func TestAddDynamic_JobFiresAfterRegistration(t *testing.T) {
	s := testScheduler(newSchedBackend())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var fired atomic.Int64
	if err := s.AddDynamic("dyn", "@every 30ms", func(_ context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("AddDynamic: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if fired.Load() == 0 {
		t.Error("AddDynamic: job never fired")
	}
}

func TestAddDynamic_DuplicateName(t *testing.T) {
	s, cancel := newManagedScheduler(t)
	defer cancel()
	err := s.AddDynamic("tick", "@every 1m", func(_ context.Context) error { return nil })
	if !errors.Is(err, dcron.ErrJobExists) {
		t.Errorf("AddDynamic duplicate: got %v; want ErrJobExists", err)
	}
}

func TestAddDynamic_InvalidSpec(t *testing.T) {
	s, cancel := newManagedScheduler(t)
	defer cancel()
	err := s.AddDynamic("bad", "not-a-cron", func(_ context.Context) error { return nil })
	if !errors.Is(err, dcron.ErrInvalidSpec) {
		t.Errorf("AddDynamic bad spec: got %v; want ErrInvalidSpec", err)
	}
}

func TestAddDynamic_NilFn(t *testing.T) {
	s, cancel := newManagedScheduler(t)
	defer cancel()
	if err := s.AddDynamic("nilf", "@every 1m", nil); !errors.Is(err, dcron.ErrNilJob) {
		t.Errorf("AddDynamic nil fn: got %v; want ErrNilJob", err)
	}
}
