// Package dcron provides a distributed cron scheduler for horizontally scaled
// Go applications.
//
// d-cron elects a single leader across replicas using PostgreSQL advisory
// locks and executes registered jobs only on the leader, so an N-replica
// deployment fires each job exactly once instead of N times.
//
// This module is under active development (v0.x) and the public API is not yet
// stable. See the SDS for the intended surface and the Phase-1 build order;
// this file currently contains only construction and lifecycle scaffolding.
package dcron

import (
	"context"
	"database/sql"
)

// Scheduler is a distributed cron scheduler.
//
// A Scheduler is created with New. It is not safe for concurrent use by
// multiple goroutines. Leadership election, the clock, and execution are
// implemented in Phase 1; for now the type holds only its resolved
// configuration.
type Scheduler struct {
	opts options
}

// New creates a Scheduler bound to the given *sql.DB connection.
//
// Options are applied in order on top of the defaults (see options.go). A nil
// db is not permitted.
func New(db *sql.DB, opts ...Option) (*Scheduler, error) {
	if db == nil {
		return nil, ErrNilDB
	}

	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	return &Scheduler{opts: cfg}, nil
}

// Start begins leadership election and job execution.
//
// The provided context governs the lifetime of background work; cancelling it
// shuts the scheduler down. Start is a no-op until Phase 1 wiring lands.
func (s *Scheduler) Start(ctx context.Context) error {
	_ = ctx
	// TODO(Phase 1): begin leadership polling and drive the clock.
	_ = s.opts

	return nil
}

// Stop drains in-flight jobs (bounded by the drain timeout) and releases
// leadership.
//
// Stop is a no-op until Phase 1 wiring lands.
func (s *Scheduler) Stop(ctx context.Context) error {
	_ = ctx
	// TODO(Phase 1): cancel polling, drain in-flight jobs, and unlock.
	_ = s.opts

	return nil
}
