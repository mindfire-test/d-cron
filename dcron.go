// Package dcron provides a distributed cron scheduler for horizontally scaled
// Go applications.
//
// d-cron elects a single leader across replicas using PostgreSQL advisory
// locks and executes registered jobs only on the leader, so an N-replica
// deployment fires each job exactly once instead of N times.
//
// Phase 1 implements the core: leadership election (internal/elector), the
// schedule clock and parser (internal/clock), and job execution with panic
// recovery, retry, and bounded drain (internal/executor). The public API is
// still evolving (v0.x); see the SDS for the intended surface.
package dcron

import (
	"container/heap"
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
	"github.com/mindfire-test/d-cron/internal/elector"
	"github.com/mindfire-test/d-cron/internal/executor"
)

// Scheduler is a distributed cron scheduler.
//
// Construct it with New, register jobs with Add, then Start it. Scheduler is
// not safe for concurrent use by multiple goroutines.
type Scheduler struct {
	opts options

	mu     sync.Mutex
	clk    *clock.Queue
	jobs   map[string]*Job
	leader *elector.Elector
	group  *executor.Group

	started bool
	runCtx  context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	// termCtx is the per-leadership-term context under which in-flight jobs
	// run. It is canceled when leadership is lost (FR-307: retries must stop,
	// a demoted leader must not keep working a fire time) and derives from
	// runCtx so shutdown cancels it too. Refreshed on each promotion.
	// Accessed only from the runLoop goroutine.
	termCtx    context.Context
	termCancel context.CancelFunc
}

// New creates a Scheduler bound to db.
//
// Options are applied in order on top of the defaults. New runs the Phase-1
// safety gates before returning:
//
//   - Session stability (SDS §3.4, issue #12): it refuses to start unless the
//     operator asserted session stability (WithSessionStableConnection) or
//     supplied a dedicated lock connection (WithDedicatedLockConn /
//     WithDedicatedLockDSN). There is deliberately NO runtime probe: measured
//     against PgBouncer, the pg_backend_pid() probe returns the same PID every
//     time at startup and would be a reliable false negative in exactly the
//     dangerous case.
//   - Pool capacity (FR-112, issue #13): when borrowing the lock connection from
//     the caller's pool, a pool with MaxOpenConnections == 1 deadlocks election
//     and is refused.
//   - TCP keepalives (issue #14): a best-effort preflight WARNs when both
//     tcp_keepalives_idle and client_connection_check_interval are 0, because a
//     dead or partitioned leader then holds the lock for hours.
//
// The dedicated connection is held for the life of the scheduler and carries
// the advisory lock. The resolved lock key is logged at INFO (issue #6).
func New(db *sql.DB, opts ...Option) (*Scheduler, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	if !cfg.sessionStable && cfg.lockConn == nil {
		return nil, ErrSessionStabilityUnasserted
	}
	if cfg.lockConn == nil {
		// Borrowing from the caller's pool: it must be able to spare one
		// connection for the leadership term (issue #13).
		if err := elector.PoolCapacity(db.Stats().MaxOpenConnections); err != nil {
			return nil, err
		}
	}
	conn, err := lockConnection(cfg, db)
	if err != nil {
		return nil, err
	}
	// Best-effort keepalive preflight; never fails startup (issue #14).
	elector.WarnKeepalive(context.Background(), elector.NewSQLConn(conn), cfg.logger)

	cfg.logger.Info("dcron: scheduler constructed",
		"namespace", cfg.namespace, "key", elector.LockKey(cfg.namespace), "instance", cfg.instance)
	return newWithBackend(elector.NewStdBackend(conn), cfg), nil
}

// lockConnection obtains the dedicated, session-stable connection used for the
// advisory lock: a caller-supplied connection (WithDedicatedLockConn /
// WithDedicatedLockDSN), or a dedicated conn borrowed from the caller's pool
// after the session-stability and pool-capacity gates have passed.
func lockConnection(cfg options, db *sql.DB) (*sql.Conn, error) {
	if cfg.lockConn != nil {
		return cfg.lockConn(context.Background())
	}
	return db.Conn(context.Background())
}

// newWithBackend builds a Scheduler around an injected Backend, bypassing the
// database gates. It is used by New and by tests.
func newWithBackend(backend elector.Backend, cfg options) *Scheduler {
	return &Scheduler{
		opts:   cfg,
		clk:    &clock.Queue{},
		jobs:   make(map[string]*Job),
		leader: elector.New(cfg.namespace, cfg.instance, backend, cfg.logger),
		done:   make(chan struct{}),
	}
}

// Key returns the resolved advisory-lock key for this scheduler's namespace
// (issue #6). Two schedulers in the same database sharing a key contend for
// one lock; use distinct namespaces per application.
func (s *Scheduler) Key() int64 { return s.leader.Key() }

// Add registers a job. name must be unique, spec must be a valid schedule (see
// clock.Parse), and fn must not be nil. Jobs must be added before Start.
func (s *Scheduler) Add(name, spec string, fn JobFunc, opts ...JobOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrAlreadyStarted
	}
	if fn == nil {
		return ErrNilJob
	}
	if _, exists := s.jobs[name]; exists {
		return ErrJobExists
	}
	var sched clock.Schedule
	var err error
	if s.opts.secondsField {
		sched, err = clock.ParseSeconds(spec, s.opts.location)
	} else {
		sched, err = clock.Parse(spec, s.opts.location)
	}
	if err != nil {
		return ErrInvalidSpec
	}
	j := &Job{name: name, sched: sched, fn: fn, overlap: true}
	for _, opt := range opts {
		opt(j)
	}
	s.jobs[name] = j
	if first := sched.Next(time.Now().In(s.opts.location)); !first.IsZero() {
		heap.Push(s.clk, &clock.Job{Name: name, FireAt: first, Sched: sched})
	}
	return nil
}

// Start begins leadership election and job execution. The context governs the
// life of the background loop; cancelling it (or calling Stop) shuts it down.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrAlreadyStarted
	}
	s.started = true
	s.runCtx, s.cancel = context.WithCancel(ctx)
	s.group = executor.NewGroup()
	// Until the first promotion no job fires; termCtx is refreshed on promotion
	// (runLoop) and canceled on demotion to abort in-flight work (FR-307).
	s.termCtx = s.runCtx
	s.termCancel = func() {}
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.runLoop()
	return nil
}

// Stop halts the poll loop, releases the advisory lock, drains in-flight jobs
// (bounded by ctx), and closes the dedicated connection. Ordering per SDS §3.6
// and issue #22: unlock → drain → close. It is safe to call once.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrNotStarted
	}
	s.started = false
	cancel := s.cancel
	s.mu.Unlock()

	cancel() // stop the poll loop (no new fires)
	<-s.done // the runLoop has exited

	var firstErr error
	// Unlock FIRST so a standby can promote while we drain (issue #11/#22).
	if err := s.leader.Release(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	// Then drain in-flight jobs, bounded by the caller's deadline.
	if err := s.group.Wait(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.leader.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
