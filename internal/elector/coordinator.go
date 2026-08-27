package elector

import "context"

// Coordinator abstracts distributed leader election and epoch generation
// so the scheduler scheduling loop is backend-agnostic (issue #49, FR-111).
//
// Contract:
//   - Acquire attempts promotion if standby/unknown, or verifies liveness if leader.
//   - Monotonic leader epoch increments on every state transition.
//   - Liveness check must NOT re-acquire or increment epoch on normal poll.
type Coordinator interface {
	Acquire(ctx context.Context) (promoted bool, epoch int64, err error)
	State() State
	IsLeader() bool
	IsDemoting() bool
	Key() int64
	FinalizeDemotion()
	Release(ctx context.Context) error
	Close() error
}

// Compile-time check that Elector implements Coordinator.
var _ Coordinator = (*Elector)(nil)
