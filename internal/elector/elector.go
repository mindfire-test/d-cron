package elector

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// State is the membership state of a scheduler in a namespace.
type State int

const (
	// StateUnknown is the zero value, before any Acquire has run (and after a
	// transient database error, see SDS §3.5). It is a real state, not a placeholder.
	StateUnknown State = iota
	StateStandby
	// StateDemoting is the transient leader-on-the-way-down state: the clock is
	// off and in-flight work is being drained or aborted before the elector
	// settles back to StateStandby. See SDS §3.5.
	StateDemoting
	StateLeader
)

// String returns a short, log-friendly label for the state.
func (s State) String() string {
	switch s {
	case StateStandby:
		return "standby"
	case StateDemoting:
		return "demoting"
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

// SingleConnectionPoolError is the typed form returned by PoolCapacity when
// maxOpen is 1 (issue #26). It carries the offending MaxOpenConnections value
// so operators and logs see the concrete misconfiguration rather than parsing
// a message. It implements errors.Is against ErrSingleConnectionPool.
type SingleConnectionPoolError struct{ MaxOpen int }

// Error implements error, naming the affected configuration (issue #26).
func (e *SingleConnectionPoolError) Error() string {
	return fmt.Sprintf("elector: database MaxOpenConnections is %d; advisory-lock election requires at least two connections", e.MaxOpen)
}

// Is allows errors.Is(err, ErrSingleConnectionPool) to match (issue #26).
func (e *SingleConnectionPoolError) Is(target error) bool {
	return target == ErrSingleConnectionPool
}

// LockKey derives the advisory-lock key for namespace.
//
// The key is the first 8 bytes of sha256("d-cron:v1:" + namespace) interpreted
// as a big-endian int64, per SDS §3.2 / issue #6, so every replica in a
// namespace contends on the same lock. Two applications sharing a database
// MUST use distinct namespaces — they would otherwise fight over one lock and
// one would never schedule anything (SDS §12 row 10).
//
// The result is never zero: pg_try_advisory_lock treats key 0 as "no lock"
// (it never blocks), so a zero hash is folded onto 1.
func LockKey(namespace string) int64 {
	h := sha256.Sum256([]byte("d-cron:v1:" + namespace))
	k := int64(binary.BigEndian.Uint64(h[:8]))
	if k == 0 {
		return 1
	}
	return k
}

// Transition is a state change emitted by the elector so the scheduler (and
// structured logs) can react without re-deriving state from Acquire's returns
// (SDS §3.5). It is delivered on the channel returned by Subscribe.
type Transition struct {
	From, To State
	Epoch    int64
	Reason   string
}

// Elector owns the advisory lock for one namespace on one dedicated backend.
// All fields are guarded by mu except backend, which is set once at
// construction and only read thereafter.
type Elector struct {
	backend  Backend
	key      int64
	ns       string
	instance string // host-unique id stamped into transition logs (SDS §3.5)
	log      *slog.Logger

	mu    sync.Mutex
	state State
	epoch int64 // fence token; increments on each promotion and each demotion
	pid   int   // backend pid of the most recent (or current) lock holder
	subs  []chan<- Transition
}

// New returns an Elector over backend for namespace. instance is a host-unique
// id recorded in transition logs. log defaults to slog.Default() when nil.
func New(namespace, instance string, backend Backend, log *slog.Logger) *Elector {
	if log == nil {
		log = slog.Default()
	}
	return &Elector{
		backend:  backend,
		key:      LockKey(namespace),
		ns:       namespace,
		instance: instance,
		log:      log,
		state:    StateUnknown,
	}
}

// Subscribe returns a channel that receives every state transition while it is
// being drained; late subscribers may miss early transitions, which is
// acceptable (State() is the source of truth).
func (e *Elector) Subscribe() <-chan Transition {
	sub := make(chan Transition, 16)
	e.mu.Lock()
	e.subs = append(e.subs, sub)
	e.mu.Unlock()
	return sub
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

// IsDemoting reports whether the elector is in the transient
// leader-on-the-way-down state, draining or aborting in-flight work.
func (e *Elector) IsDemoting() bool { return e.State() == StateDemoting }

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

// emit broadcasts a transition to every subscriber without blocking. A slow
// subscriber drops transitions rather than stalling the elector (Acquire's
// state is always authoritative). Must hold mu.
func (e *Elector) emit(from, to State, reason string) {
	t := Transition{From: from, To: to, Epoch: e.epoch, Reason: reason}
	for _, sub := range e.subs {
		select {
		case sub <- t:
		default:
		}
	}
}

// Acquire is the leadership-polling step (SDS §3.5). On a standby/unknown it
// attempts to win the lock with pg_try_advisory_lock (never the blocking form);
// on a leader it re-confirms ownership with a read-only pg_locks probe and never
// re-acquires — advisory locks are re-entrant, so re-try_locking would mask a
// lost lock and break the single explicit unlock on shutdown (C-07). err is
// surfaced to the caller so a transient database failure is logged and retried
// rather than crashing the host (NFR-202).
func (e *Elector) Acquire(ctx context.Context) (promoted bool, epoch int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch e.state {
	case StateLeader:
		holds, err := e.backend.HoldsLock(ctx, e.key)
		if err != nil {
			// Database unreachable/transient (NFR-202): do not crash the host.
			// Demote; the next poll re-acquires if the session still owns the
			// lock, or resumes election once the database recovers.
			e.demote("db_unavailable")
			return false, e.epoch, err
		}
		if holds {
			return false, e.epoch, nil
		}
		// Lost the lock while leading (partitioned/killed backend, or a
		// concurrent release). Step down so a future Acquire can re-win.
		e.demote("lost_lock")
		return false, e.epoch, nil
	case StateDemoting:
		// Settling out of leadership; the scheduler finalizes, then re-polls.
		return false, e.epoch, nil
	default: // StateUnknown or StateStandby
		acquired, pid, err := e.backend.TryLock(ctx, e.key)
		if err != nil {
			return false, e.epoch, err
		}
		if acquired {
			e.state = StateLeader
			e.epoch++
			e.pid = pid
			e.log.Info("elector: became leader",
				"namespace", e.ns, "instance", e.instance, "key", e.key,
				"epoch", e.epoch, "pid", pid)
			e.emit(StateStandby, StateLeader, "promoted")
			return true, e.epoch, nil
		}
		// The lock is held by a different replica (or was not yet released). A
		// failed attempt is the standby's steady state, not "unknown".
		e.state = StateStandby
		return false, e.epoch, nil
	}
}

// demote begins stepping a leader down: bump the epoch fence token so any
// in-flight work carrying the old epoch is recognised as stale, drop the pid,
// and emit LEADER -> DEMOTING. The scheduler finalizes to STANDBY once it has
// aborted/drained in-flight work (FR-307). Must hold mu.
func (e *Elector) demote(reason string) {
	if e.state != StateLeader {
		return
	}
	e.epoch++
	e.pid = 0
	e.state = StateDemoting
	e.log.Warn("elector: lost leadership",
		"namespace", e.ns, "instance", e.instance, "key", e.key,
		"epoch", e.epoch, "reason", reason)
	e.emit(StateLeader, StateDemoting, reason)
}

// FinalizeDemotion completes a LEADER -> DEMOTING transition by settling to
// STANDBY, emitting DEMOTING -> STANDBY. Safe to call from the scheduler's
// demotion path without first checking state. Must hold mu.
func (e *Elector) FinalizeDemotion() {
	if e.state != StateDemoting {
		return
	}
	e.emit(StateDemoting, StateStandby, "drained")
	e.state = StateStandby
}

// Release frees the advisory lock. It is a no-op for standbys and may be
// called at most once by the former leader on graceful shutdown (SDS §3.6). The
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
	released, err := e.backend.Release(ctx, key)
	if err != nil {
		return err
	}
	if !released {
		// false means we did not hold the lock (already released by the server
		// on backend exit, or never acquired) — log it; not fatal (SDS §3.6).
		e.log.Warn("elector: unlock reported not-held",
			"namespace", e.ns, "instance", e.instance, "key", e.key)
	}
	return nil
}

// Close closes the dedicated connection backing this elector. It must be
// called after Release, typically from the scheduler's Stop path.
func (e *Elector) Close() error {
	if e.backend == nil {
		return nil
	}
	return e.backend.Close()
}
