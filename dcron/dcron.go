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
	"fmt"
	"sync"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
	"github.com/mindfire-test/d-cron/internal/elector"
	"github.com/mindfire-test/d-cron/internal/executor"
	"github.com/mindfire-test/d-cron/internal/store"
)

// Scheduler is a distributed cron scheduler.
//
// Construct it with New, register jobs with Add, then Start it. Scheduler is
// not safe for concurrent use by multiple goroutines.
type Scheduler struct {
	opts options

	db *sql.DB

	store *store.Store

	mu     sync.Mutex
	clk    *clock.Queue
	jobs   map[string]*Job
	leader *elector.Elector
	group  *executor.Group

	started bool
	runCtx  context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	termCtx    context.Context
	termCancel context.CancelFunc

	lastPrune time.Time
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
		return nil, &SessionStabilityError{}
	}
	if cfg.lockConn == nil {
		if err := elector.PoolCapacity(db.Stats().MaxOpenConnections); err != nil {
			return nil, err
		}
	}
	conn, err := lockConnection(cfg, db)
	if err != nil {
		return nil, err
	}

	elector.WarnKeepalive(context.Background(), elector.NewSQLConn(conn), cfg.logger)

	var hist *store.Store
	if cfg.history && db != nil {
		hist, err = store.New(db, cfg.schema)
		if err != nil {
			return nil, err
		}
		if err := store.Migrate(context.Background(), db, cfg.schema); err != nil {
			return nil, err
		}
		cfg.logger.Info("dcron: history enabled",
			"schema", cfg.schema, "retention", cfg.retention.String())
	}

	cfg.logger.Info("dcron: scheduler constructed",
		"namespace", cfg.namespace, "key", elector.LockKey(cfg.namespace), "instance", cfg.instance)
	return newWithBackend(elector.NewStdBackend(conn), db, cfg, hist), nil
}

func lockConnection(cfg options, db *sql.DB) (*sql.Conn, error) {
	if cfg.lockConn != nil {
		return cfg.lockConn(context.Background())
	}
	return db.Conn(context.Background())
}

func newWithBackend(backend elector.Backend, db *sql.DB, cfg options, hist *store.Store) *Scheduler {
	return &Scheduler{
		opts:   cfg,
		db:     db,
		store:  hist,
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

// InstanceID returns this scheduler's process-local identifier as it appears
// in leadership-transition logs and history rows (issue #38: the dashboard
// displays it alongside leadership state).
func (s *Scheduler) InstanceID() string { return s.opts.instance }

// Namespace returns the namespace this scheduler was constructed with.
func (s *Scheduler) Namespace() string { return s.opts.namespace }

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
		return &JobExistsError{Name: name}
	}
	var sched clock.Schedule
	var err error
	if s.opts.secondsField {
		sched, err = clock.ParseSeconds(spec, s.opts.location)
	} else {
		sched, err = clock.Parse(spec, s.opts.location)
	}
	if err != nil {
		return &InvalidSpecError{Name: name, Spec: spec}
	}
	j := &Job{name: name, spec: spec, sched: sched, fn: fn, overlap: true}
	for _, opt := range opts {
		opt(j)
	}
	if j.missedPolicy == MissedCatchUp && s.store == nil {
		return fmt.Errorf("dcron: MissedCatchUp policy requires history store (WithHistory option)")
	}
	s.jobs[name] = j
	if first := sched.Next(time.Now().In(s.opts.location)); !first.IsZero() {
		j.nextRun = first
		heap.Push(s.clk, &clock.Job{Name: name, FireAt: first, Sched: sched})
	}
	return nil
}

// AddOnce registers a job that fires exactly once at a fixed instant (issue
// #33, FR-209) and is then evicted from the schedule. Everything else matches
// Add: name must be unique and fn must not be nil. The once schedule is
// deliberately not persisted — like every registration it is re-registered on
// process restart (Phase 2 is in-memory by design).
func (s *Scheduler) AddOnce(name string, at time.Time, fn JobFunc, opts ...JobOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrAlreadyStarted
	}
	if fn == nil {
		return ErrNilJob
	}
	if _, exists := s.jobs[name]; exists {
		return &JobExistsError{Name: name}
	}
	sched := clock.NewOnce(at, s.opts.location)
	if !sched.Next(time.Now().In(s.opts.location)).IsZero() {
		j := &Job{name: name, spec: "@once " + at.Format(time.RFC3339), sched: sched, fn: fn, overlap: true}
		for _, opt := range opts {
			opt(j)
		}
		s.jobs[name] = j
		j.nextRun = at.In(s.opts.location)
		heap.Push(s.clk, &clock.Job{Name: name, FireAt: j.nextRun, Sched: sched})
	} else {
		return &InvalidSpecError{Name: name, Spec: "@once " + at.Format(time.RFC3339)}
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

	cancel()
	<-s.done

	var firstErr error

	if err := s.leader.Release(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	if err := s.group.Wait(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.leader.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
