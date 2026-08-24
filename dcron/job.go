package dcron

import (
	"context"
	"sync"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
	"github.com/mindfire-test/d-cron/internal/executor"
)

// JobFunc is the signature of a job's execution function. It receives only a
// context and returns only an error, which keeps the scheduler free of
// payloads and serialisation (see the SDS).
type JobFunc func(ctx context.Context) error

// Job is a registered, schedulable unit of work.
//
// Jobs are created through (Scheduler).Add and are not intended to be
// constructed directly. All fields are internal; callers only need the job
// name to reason about a registration.
type Job struct {
	name    string
	spec    string
	sched   clock.Schedule
	fn      JobFunc
	retry   executor.Retry
	overlap bool
	busy    sync.Mutex

	// Phase 2 status tracking (guarded by statusMu). Guarded separately from
	// s.mu because jobs complete on the executor's goroutines, not the loop
	// goroutine, and we never want to block the leadership loop updating one
	// job's status while another is being inspected.
	statusMu     sync.Mutex
	nextRun      time.Time
	lastRun      time.Time
	lastOutcome  string
	lastError    string
	lastDuration time.Duration
	running      bool
}

// Name returns the job's unique identifier.
func (j *Job) Name() string { return j.name }

// JobOption configures an individual job at registration time.
type JobOption func(*Job)

// Retry configures how a job is retried after failure (SDS §5.3, issue #20).
// Zero fields fall back to the documented defaults: 5 attempts, a 1s base
// backoff doubling up to a 5-minute cap, jitter enabled, and a 30-minute
// per-attempt timeout. Retries are in-memory and are lost on process restart;
// they abort immediately if leadership is lost mid-sequence (FR-307).
type Retry struct {
	// Attempts is the total number of runs (>=1). 1 disables retry.
	Attempts int
	// Backoff is the delay after the first failure before the first retry.
	Backoff time.Duration
	// Factor multiplies the backoff between retries; 2 is the default.
	Factor float64
	// MaxBackoff caps the backoff.
	MaxBackoff time.Duration
	// Jitter adds up to ±25% jitter to each backoff to avoid thundering
	// herds across replicas. Defaults to true.
	Jitter bool
	// Timeout bounds a single execution attempt (SDS §5.2). The deadline is
	// honoured by cancelling the job's context; a job that ignores its context
	// runs on. Defaults to 30 minutes.
	Timeout time.Duration
}

// WithTimeout bounds a single execution of the job. The deadline is honoured
// by cancelling the job's context; a job that ignores its context runs on.
// The default is 30 minutes (SDS §5.2, issue #19).
func WithTimeout(d time.Duration) JobOption {
	return func(j *Job) { j.retry.Timeout = d }
}

// WithRetry overrides the default retry behaviour for a job (SDS §5.3, issue
// #20). Zero fields fall back to the defaults documented on Retry.
func WithRetry(r Retry) JobOption {
	return func(j *Job) {
		j.retry = executor.Retry{
			Attempts:   r.Attempts,
			Backoff:    r.Backoff,
			Factor:     r.Factor,
			MaxBackoff: r.MaxBackoff,
			Jitter:     r.Jitter,
			Timeout:    r.Timeout,
		}
	}
}

// WithNoOverlap suppresses firing a job while its previous run is still
// active. Overlap is allowed by default.
func WithNoOverlap() JobOption {
	return func(j *Job) { j.overlap = false }
}
