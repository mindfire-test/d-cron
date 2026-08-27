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
// OverlapPolicy governs behaviour when a fire time is reached while a previous
// run of the same job is still active (SDS §7.2, issue #44, FR-308).
type OverlapPolicy int

const (
	// OverlapSkip skips the new fire time (default per SRS FR-308).
	OverlapSkip OverlapPolicy = iota
	// OverlapQueue queues a single run to execute after the current run finishes.
	OverlapQueue
	// OverlapAllow permits concurrent executions of the same job.
	OverlapAllow
)

// MissedRunPolicy governs behaviour when scheduled fire times were missed
// (e.g. during an outage or failover) (SDS §7.3, issue #45, FR-312).
type MissedRunPolicy int

const (
	// MissedSkip skips missed fire times (default per SRS FR-312).
	MissedSkip MissedRunPolicy = iota
	// MissedCatchUp dispatches missed fire times within a lookback window up to a cap.
	MissedCatchUp
)

type Job struct {
	name          string
	spec          string
	sched         clock.Schedule
	fn            JobFunc
	retry         executor.Retry
	overlap       bool
	overlapPolicy OverlapPolicy
	queuedRun     bool
	missedPolicy  MissedRunPolicy
	maxLookback   time.Duration
	maxCatchUp    int
	busy          sync.Mutex

	statusMu     sync.Mutex
	nextRun      time.Time
	lastRun      time.Time
	lastOutcome  string
	lastError    string
	lastDuration time.Duration
	running      bool
	paused       bool
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
// active. Same as WithOverlapPolicy(OverlapSkip).
func WithNoOverlap() JobOption {
	return func(j *Job) {
		j.overlapPolicy = OverlapSkip
		j.overlap = false
	}
}

// WithOverlapPolicy sets the overlap policy for a job (SDS §7.2, issue #44, FR-308).
// Options are OverlapSkip (default), OverlapQueue, and OverlapAllow.
func WithOverlapPolicy(p OverlapPolicy) JobOption {
	return func(j *Job) {
		j.overlapPolicy = p
		j.overlap = (p == OverlapAllow)
	}
}

// WithMissedRunPolicy sets the missed-run policy for a job (SDS §7.3, issue #45, FR-312).
// Options are MissedSkip (default) and MissedCatchUp.
func WithMissedRunPolicy(p MissedRunPolicy) JobOption {
	return func(j *Job) { j.missedPolicy = p }
}

// WithMaxLookback caps how far back in time MissedCatchUp will search for missed runs.
func WithMaxLookback(d time.Duration) JobOption {
	return func(j *Job) { j.maxLookback = d }
}

// WithMaxCatchUpRuns caps the maximum number of catch-up executions dispatched for a job.
func WithMaxCatchUpRuns(maxRuns int) JobOption {
	return func(j *Job) { j.maxCatchUp = maxRuns }
}

// WithSinceLastSuccess schedules the next fire time relative to the completion
// of the last successful execution (issue #46, FR-210). Requires history store.
func WithSinceLastSuccess(d time.Duration) JobOption {
	return func(j *Job) {
		j.sched = clock.SinceSuccessSchedule{Interval: d}
		j.spec = "@since_success " + d.String()
	}
}
