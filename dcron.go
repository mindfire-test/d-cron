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
}

// New creates a Scheduler bound to db.
//
// Options are applied in order on top of the defaults. New runs the Phase-1
// safety gates before returning: it refuses a pool whose MaxOpenConnections is
// 1 (FR-112), and -- unless WithDedicatedLockConn or WithSessionStableConnection
// was set -- it borrows a dedicated connection and probes session stability,
// refusing a transaction-mode pooler (FR-108, SDS §3.4). The dedicated
// connection is held for the life of the scheduler and carries the advisory
// lock.
func New(db *sql.DB, opts ...Option) (*Scheduler, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := elector.PoolCapacity(db.Stats().MaxOpenConnections); err != nil {
		return nil, err
	}
	conn, err := lockConnection(cfg, db)
	if err != nil {
		return nil, err
	}
	return newWithBackend(elector.NewStdBackend(conn), cfg), nil
}

// lockConnection obtains the dedicated, session-stable connection used for the
// advisory lock: a caller-supplied connection (WithDedicatedLockConn), or a
// dedicated conn borrowed from db, probed for session stability unless the
// operator asserted it (WithSessionStableConnection).
func lockConnection(cfg options, db *sql.DB) (*sql.Conn, error) {
	if cfg.lockConn != nil {
		return cfg.lockConn(context.Background())
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	if cfg.sessionStable {
		return conn, nil
	}
	stable, err := elector.ProbeSessionStable(context.Background(), elector.NewSQLConn(conn))
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !stable {
		conn.Close()
		return nil, elector.ErrPoolerSessionInstability
	}
	return conn, nil
}

// newWithBackend builds a Scheduler around an injected Backend, bypassing the
// database gates. It is used by New and by tests.
func newWithBackend(backend elector.Backend, cfg options) *Scheduler {
	return &Scheduler{
		opts:   cfg,
		clk:    &clock.Queue{},
		jobs:   make(map[string]*Job),
		leader: elector.New(cfg.namespace, backend, cfg.logger),
		done:   make(chan struct{}),
	}
}

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
	sched, err := clock.Parse(spec, s.opts.location)
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
	s.group = executor.NewGroup(s.runCtx)
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.runLoop()
	return nil
}

// Stop halts the poll loop, drains in-flight jobs (bounded by ctx), and
// releases the advisory lock. It is safe to call once.
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
	if err := s.group.Wait(ctx); err != nil {
		firstErr = err
	}
	if err := s.leader.Release(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.leader.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
