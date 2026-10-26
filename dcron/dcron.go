// Package dcron provides a distributed cron scheduler for Go applications.
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
type Scheduler struct {
	opts options

	db *sql.DB

	store *store.Store

	mu           sync.Mutex
	currentEpoch int64
	clk          *clock.Queue
	jobs         map[string]*Job
	leader       elector.Coordinator
	group        *executor.Group

	started bool
	runCtx  context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	termCtx    context.Context
	termCancel context.CancelFunc

	lastPrune time.Time
}

// New creates a Scheduler bound to db.
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
	startupTimeout := cfg.startupTimeout
	if startupTimeout <= 0 {
		startupTimeout = 15 * time.Second
	}
	startupCtx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	conn, err := lockConnection(startupCtx, cfg, db)
	if err != nil {
		return nil, err
	}

	elector.WarnKeepalive(startupCtx, elector.NewSQLConn(conn), cfg.logger)

	var hist *store.Store
	if cfg.history && db != nil {
		hist, err = store.New(db, cfg.schema)
		if err != nil {
			return nil, err
		}
		if err := store.Migrate(startupCtx, db, cfg.schema); err != nil {
			return nil, err
		}
		cfg.logger.Info("dcron: history enabled",
			"schema", cfg.schema, "retention", cfg.retention.String())
	}

	cfg.logger.Info("dcron: scheduler constructed",
		"namespace", cfg.namespace, "key", elector.LockKey(cfg.namespace), "instance", cfg.instance)
	return newWithBackend(elector.NewStdBackend(conn), db, cfg, hist), nil
}

func lockConnection(ctx context.Context, cfg options, db *sql.DB) (*sql.Conn, error) {
	if cfg.lockConn != nil {
		return cfg.lockConn(ctx)
	}
	return db.Conn(ctx)
}

func newWithBackend(backend elector.Backend, db *sql.DB, cfg options, hist *store.Store) *Scheduler {
	el := elector.New(cfg.namespace, cfg.instance, backend, cfg.logger)
	if cfg.pollTimeout > 0 {
		el.SetProbeTimeout(cfg.pollTimeout)
	}
	return &Scheduler{
		opts:   cfg,
		db:     db,
		store:  hist,
		clk:    &clock.Queue{},
		jobs:   make(map[string]*Job),
		leader: el,
		done:   make(chan struct{}),
	}
}

// Key returns the resolved advisory-lock key for this scheduler's namespace.
func (s *Scheduler) Key() int64 { return s.leader.Key() }

// InstanceID returns this scheduler's process-local identifier.
func (s *Scheduler) InstanceID() string { return s.opts.instance }

// Namespace returns the namespace this scheduler was constructed with.
func (s *Scheduler) Namespace() string { return s.opts.namespace }

// Epoch returns the current authoritative leader epoch (0 if not leader).
func (s *Scheduler) Epoch() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentEpoch
}

// Add registers a job with the scheduler.
func (s *Scheduler) Add(name, spec string, fn JobFunc, opts ...JobOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrAlreadyStarted
	}
	return s.registerJobLocked(name, spec, fn, opts...)
}

func (s *Scheduler) registerJobLocked(name, spec string, fn JobFunc, opts ...JobOption) error {
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
	if _, ok := j.sched.(clock.SinceSuccessSchedule); ok && s.store == nil {
		return fmt.Errorf("dcron: WithSinceLastSuccess requires history store (WithHistory option)")
	}
	s.jobs[name] = j
	if first := j.sched.Next(time.Now().In(s.opts.location)); !first.IsZero() {
		j.nextRun = first
		heap.Push(s.clk, &clock.Job{Name: name, FireAt: first, Sched: j.sched})
	}
	return nil
}

// AddOnce registers a job that fires exactly once at a fixed instant.
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

// Start begins leadership election and job execution.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrAlreadyStarted
	}
	s.started = true
	s.runCtx, s.cancel = context.WithCancel(ctx)
	s.group = executor.NewGroup()

	s.termCtx, s.termCancel = context.WithCancel(s.runCtx)
	s.termCancel() // Cancelled until first promotion
	s.currentEpoch = 0
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.runLoop()
	return nil
}

// Stop halts the scheduler loop and releases resources.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrNotStarted
	}
	s.started = false
	cancel := s.cancel
	s.termCancel()
	s.currentEpoch = 0
	s.mu.Unlock()

	cancel()
	<-s.done

	var firstErr error

	if err := s.leader.Release(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	drainCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && s.opts.drainTimeout > 0 {
		var cancelDrain context.CancelFunc
		drainCtx, cancelDrain = context.WithTimeout(ctx, s.opts.drainTimeout)
		defer cancelDrain()
	}

	if err := s.group.Wait(drainCtx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.leader.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Pause prevents a registered job from being dispatched on future fire times.
func (s *Scheduler) Pause(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started && s.Leadership() == LeadershipStandby {
		return ErrNotLeader
	}
	j, ok := s.jobs[name]
	if !ok {
		return &JobNotFoundError{Name: name}
	}
	j.statusMu.Lock()
	j.paused = true
	j.statusMu.Unlock()
	s.opts.logger.Info("dcron: job paused", "job", name)
	return nil
}

// Resume re-enables a previously paused job.
func (s *Scheduler) Resume(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started && s.Leadership() == LeadershipStandby {
		return ErrNotLeader
	}
	j, ok := s.jobs[name]
	if !ok {
		return &JobNotFoundError{Name: name}
	}
	j.statusMu.Lock()
	j.paused = false
	j.statusMu.Unlock()
	s.opts.logger.Info("dcron: job resumed", "job", name)
	return nil
}

// Remove unregisters a job at runtime.
func (s *Scheduler) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started && s.Leadership() == LeadershipStandby {
		return ErrNotLeader
	}
	if _, ok := s.jobs[name]; !ok {
		return &JobNotFoundError{Name: name}
	}
	delete(s.jobs, name)
	s.opts.logger.Info("dcron: job removed", "job", name)
	return nil
}

// AddDynamic registers a new job at runtime after Start has been called.
func (s *Scheduler) AddDynamic(name, spec string, fn JobFunc, opts ...JobOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started && s.Leadership() == LeadershipStandby {
		return ErrNotLeader
	}
	if err := s.registerJobLocked(name, spec, fn, opts...); err != nil {
		return err
	}
	s.opts.logger.Info("dcron: job added dynamically", "job", name, "spec", spec)
	return nil
}
