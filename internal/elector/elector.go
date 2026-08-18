package elector

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"sync"
)

// State is the membership state of a scheduler in a namespace.
type State int

const (
	// StateUnknown is the zero value, before any Acquire has run.
	StateUnknown State = iota
	StateStandby
	StateLeader
)

// String returns a short, log-friendly label for the state.
func (s State) String() string {
	switch s {
	case StateStandby:
		return "standby"
	case StateLeader:
		return "leader"
	default:
		return "unknown"
	}
}

// Sentinel errors returned by the elector and its construction gates.
var (
	// ErrNotLeader is returned when an operation requires leadership but the
	// elector is not in the leader state.
	ErrNotLeader = errors.New("elector: not the leader")

	// ErrPoolerSessionInstability is returned at construction when the
	// configured connection maps to a different Postgres backend per statement
	// (a transaction-mode pooler), which makes advisory locks unreliable.
	ErrPoolerSessionInstability = errors.New("elector: connection is not session-stable; use a session-mode pooler or a dedicated connection")

	// ErrSingleConnectionPool is returned at construction when MaxOpenConnections
	// is 1, which starves both the lock and the session-stability probe.
	ErrSingleConnectionPool = errors.New("elector: database MaxOpenConnections is 1; advisory-lock election requires at least two connections")
)

// LockKey derives the advisory-lock key for namespace.
//
// The derivation is deterministic so every replica in a namespace contends on
// the same lock. The result is never zero: pg_try_advisory_lock treats key 0
// as "no lock" (it never blocks), so a zero hash is folded onto 1.
func LockKey(namespace string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(namespace))
	k := int64(h.Sum64())
	if k == 0 {
		return 1
	}
	return k
}

// Elector owns the advisory lock for one namespace on one dedicated backend.
// All fields are guarded by mu except backend, which is set once at
// construction and only read thereafter.
type Elector struct {
	backend Backend
	key     int64
	ns      string
	log     *slog.Logger

	mu    sync.Mutex
	state State
	epoch int64 // fence token; increments on each promotion, monotonic
	pid   int   // backend pid of the most recent (or current) lock holder
}

// New returns an Elector over backend for namespace. log defaults to
// slog.Default() when nil. Acquire must be polled by the scheduler to drive
// the state machine.
func New(namespace string, backend Backend, log *slog.Logger) *Elector {
	if log == nil {
		log = slog.Default()
	}
	return &Elector{
		backend: backend,
		key:     LockKey(namespace),
		ns:      namespace,
		log:     log,
		state:   StateUnknown,
	}
}

// Namespace returns the namespace this elector is bound to.
func (e *Elector) Namespace() string { return e.ns }

// Key returns the advisory-lock key for this elector's namespace.
func (e *Elector) Key() int64 { return e.key }

// State returns the current membership state under its lock.
func (e *Elector) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// IsLeader reports whether the elector currently holds leadership.
func (e *Elector) IsLeader() bool { return e.State() == StateLeader }

// PID returns the backend pid of the most recent (or current) lock holder.
func (e *Elector) PID() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pid
}

// Epoch returns the current leader epoch (fencing token).
func (e *Elector) Epoch() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.epoch
}

// Acquire is the leadership-polling step. It either wins the lock (promoting
// standby -> leader and bumping the epoch fence token) or, if already leader,
// re-confirms ownership with a read-only probe (never re-acquiring, per SDS
// §3.5). Returns the current epoch for all outcomes.
func (e *Elector) Acquire(ctx context.Context) (promoted bool, epoch int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch e.state {
	case StateLeader:
		// Re-confirm, never re-acquire: re-acquiring an advisory lock is
		// re-entrant (it returns true with no net state change) and would
		// mask a lock we actually lost.
		holds, err := e.backend.HoldsLock(ctx, e.key)
		if err != nil {
			return false, e.epoch, err
		}
		if holds {
			return false, e.epoch, nil
		}
		// Lost the lock while leading (partitioned/killed backend, or a
		// concurrent release). Step down so a future Acquire can re-win
		// cleanly and the stale epoch is no longer trusted.
		e.log.Warn("elector: lost leadership while holding it",
			"namespace", e.ns, "key", e.key)
		e.state = StateStandby
		return false, e.epoch, nil
	default: // StateStandby or StateUnknown
		acquired, pid, err := e.backend.TryLock(ctx, e.key)
		if err != nil {
			return false, e.epoch, err
		}
		if acquired {
			e.state = StateLeader
			e.epoch++
			e.pid = pid
			e.log.Info("elector: became leader",
				"namespace", e.ns, "key", e.key, "epoch", e.epoch, "pid", pid)
			return true, e.epoch, nil
		}
		// The lock is held by a different replica (or was not yet released). A
		// failed attempt is the standby's steady state, not "unknown".
		e.state = StateStandby
		return false, e.epoch, nil
	}
}

// Release frees the advisory lock. It is a no-op for standbys and may be
// called at most once by the former leader on shutdown (SDS §3.3). The
// connection itself is closed separately by Close.
func (e *Elector) Release(ctx context.Context) error {
	e.mu.Lock()
	state := e.state
	key := e.key
	e.state = StateStandby
	e.pid = 0
	e.mu.Unlock()

	if state != StateLeader {
		return nil
	}
	return e.backend.Release(ctx, key)
}

// Close closes the dedicated connection backing this elector. It must be
// called after Release, typically from the scheduler's Stop path.
func (e *Elector) Close() error {
	if e.backend == nil {
		return nil
	}
	return e.backend.Close()
}
