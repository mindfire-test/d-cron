package elector_test

// Coordinator conformance test suite (issue #52, FR-111).
//
// Every Coordinator implementation (Elector backed by stdBackend, MemoryCoordinator,
// and future Redis / etcd backends) MUST pass the table-driven cases defined in
// RunCoordinatorConformance. To add a new backend to the suite, call
// RunCoordinatorConformance from a test in the backend's own package.

import (
	"context"
	"sync"
	"testing"

	"github.com/mindfire-test/d-cron/internal/elector"
)

// fakeBackendCoord is a trivial in-memory Backend for wiring a real *Elector in
// conformance tests without Postgres.
type fakeBackendCoord struct {
	mu   sync.Mutex
	held bool
}

func (b *fakeBackendCoord) TryLock(_ context.Context, _ int64) (bool, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held {
		return false, 0, nil
	}
	b.held = true
	return true, 1, nil
}

func (b *fakeBackendCoord) HoldsLock(_ context.Context, _ int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held, nil
}

func (b *fakeBackendCoord) Release(_ context.Context, _ int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	was := b.held
	b.held = false
	return was, nil
}

func (b *fakeBackendCoord) Close() error { return nil }

// RunCoordinatorConformance runs the standard Coordinator contract test suite
// against any elector.Coordinator implementation. factory must return a freshly
// constructed, un-acquired Coordinator on each call.
func RunCoordinatorConformance(t *testing.T, factory func() elector.Coordinator) {
	t.Helper()

	t.Run("initial_state_is_unknown_or_standby", func(t *testing.T) {
		c := factory()
		st := c.State()
		if st == elector.StateLeader {
			t.Errorf("initial state is leader; want Unknown or Standby")
		}
	})

	t.Run("first_acquire_promotes_to_leader", func(t *testing.T) {
		c := factory()
		promoted, epoch, err := c.Acquire(context.Background())
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if !promoted {
			t.Errorf("first Acquire: promoted=false; want true")
		}
		if epoch <= 0 {
			t.Errorf("first Acquire: epoch=%d; want >0", epoch)
		}
		if !c.IsLeader() {
			t.Errorf("after Acquire: IsLeader=false; want true")
		}
	})

	t.Run("second_acquire_is_liveness_noop", func(t *testing.T) {
		c := factory()
		_, epochA, _ := c.Acquire(context.Background())
		promoted, epochB, err := c.Acquire(context.Background())
		if err != nil {
			t.Fatalf("second Acquire: %v", err)
		}
		if promoted {
			t.Errorf("second Acquire: promoted=true; want false (already leader)")
		}
		if epochB != epochA {
			t.Errorf("second Acquire: epoch changed %d→%d; want stable", epochA, epochB)
		}
	})

	t.Run("release_demotes_leader", func(t *testing.T) {
		c := factory()
		if _, _, err := c.Acquire(context.Background()); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if err := c.Release(context.Background()); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if c.IsLeader() {
			t.Errorf("after Release: IsLeader=true; want false")
		}
	})

	t.Run("key_is_nonzero", func(t *testing.T) {
		c := factory()
		if c.Key() == 0 {
			t.Errorf("Key()=0; advisory lock key 0 is treated as 'no-lock' by Postgres")
		}
	})

	t.Run("finalize_demotion_settles_to_standby", func(t *testing.T) {
		c := factory()
		if _, _, err := c.Acquire(context.Background()); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if err := c.Release(context.Background()); err != nil {
			t.Fatalf("Release: %v", err)
		}
		// Some coordinators transition through StateDemoting; call Finalize to settle.
		c.FinalizeDemotion()
		if st := c.State(); st == elector.StateDemoting {
			t.Errorf("after FinalizeDemotion: state=Demoting; want Standby or Unknown")
		}
	})

	t.Run("close_is_idempotent", func(t *testing.T) {
		c := factory()
		if err := c.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	})
}

// TestMemoryCoordinator_Conformance runs the full conformance suite against the
// in-process MemoryCoordinator (issue #52).
func TestMemoryCoordinator_Conformance(t *testing.T) {
	RunCoordinatorConformance(t, func() elector.Coordinator {
		return elector.NewMemoryCoordinator("test-ns", nil)
	})
}

// TestElector_Conformance runs the full conformance suite against the production
// *Elector wired to a fakeBackend so no Postgres instance is needed.
func TestElector_Conformance(t *testing.T) {
	RunCoordinatorConformance(t, func() elector.Coordinator {
		backend := &fakeBackendCoord{}
		return elector.New("test-ns", "i-1", backend, nil)
	})
}
