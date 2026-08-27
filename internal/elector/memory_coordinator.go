package elector

import (
	"context"
	"log/slog"
	"sync"
)

// MemoryCoordinator is an in-process implementation of [Coordinator] that uses a
// simple mutex instead of PostgreSQL advisory locks (issue #52, FR-111).
//
// It is designed for:
//   - Single-replica deployments where no real coordination is required.
//   - Unit and integration tests that do not have a Postgres instance available.
//
// A MemoryCoordinator always wins the election on first Acquire (it is the only
// replica in the process) and never loses leadership unless Close or Release
// are called explicitly.
type MemoryCoordinator struct {
	mu    sync.Mutex
	state State
	epoch int64
	key   int64
	log   *slog.Logger
	subs  []chan<- Transition
}

// NewMemoryCoordinator returns a MemoryCoordinator for namespace.
// log defaults to slog.Default() when nil.
func NewMemoryCoordinator(namespace string, log *slog.Logger) *MemoryCoordinator {
	if log == nil {
		log = slog.Default()
	}
	return &MemoryCoordinator{
		key:   LockKey(namespace),
		state: StateUnknown,
		log:   log,
	}
}

// Subscribe returns a channel that receives every state transition.
func (m *MemoryCoordinator) Subscribe() <-chan Transition {
	sub := make(chan Transition, 16)
	m.mu.Lock()
	m.subs = append(m.subs, sub)
	m.mu.Unlock()
	return sub
}

func (m *MemoryCoordinator) emit(from, to State, reason string) {
	t := Transition{From: from, To: to, Epoch: m.epoch, Reason: reason}
	for _, sub := range m.subs {
		select {
		case sub <- t:
		default:
		}
	}
}

// Acquire implements [Coordinator]. On the first call it transitions to leader
// and increments the epoch. Subsequent calls are a no-op liveness check that
// always reports the lock is held.
func (m *MemoryCoordinator) Acquire(_ context.Context) (promoted bool, epoch int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case StateLeader:
		// Already leader — liveness trivially passes.
		return false, m.epoch, nil
	case StateDemoting:
		return false, m.epoch, nil
	default:
		m.epoch++
		m.state = StateLeader
		m.log.Info("memory_coordinator: became leader", "epoch", m.epoch)
		m.emit(StateStandby, StateLeader, "promoted")
		return true, m.epoch, nil
	}
}

// State returns the current membership state.
func (m *MemoryCoordinator) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// IsLeader reports whether the coordinator currently holds leadership.
func (m *MemoryCoordinator) IsLeader() bool { return m.State() == StateLeader }

// IsDemoting reports whether the coordinator is draining after demotion.
func (m *MemoryCoordinator) IsDemoting() bool { return m.State() == StateDemoting }

// Key returns the advisory-lock key derived from the namespace.
func (m *MemoryCoordinator) Key() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.key
}

// FinalizeDemotion implements [Coordinator].
func (m *MemoryCoordinator) FinalizeDemotion() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateDemoting {
		return
	}
	m.emit(StateDemoting, StateStandby, "drained")
	m.state = StateStandby
}

// Release demotes the coordinator to standby (implements [Coordinator]).
func (m *MemoryCoordinator) Release(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateLeader {
		m.epoch++
		m.state = StateDemoting
		m.log.Info("memory_coordinator: released leadership", "epoch", m.epoch)
		m.emit(StateLeader, StateDemoting, "released")
	}
	return nil
}

// Close is a no-op for the in-memory coordinator (implements [Coordinator]).
func (m *MemoryCoordinator) Close() error { return nil }

// Compile-time check that MemoryCoordinator implements Coordinator.
var _ Coordinator = (*MemoryCoordinator)(nil)
