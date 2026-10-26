package dcron

import (
	"context"
	"sync"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
	"github.com/mindfire-test/d-cron/internal/executor"
)

// JobFunc is the signature of a job's execution function.
type JobFunc func(ctx context.Context) error

// Job is a registered, schedulable unit of work.
type OverlapPolicy int

const (
	OverlapSkip OverlapPolicy = iota
	OverlapQueue
	OverlapAllow
)

// MissedRunPolicy governs behaviour when scheduled fire times were missed.
type MissedRunPolicy int

const (
	MissedSkip MissedRunPolicy = iota
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
	missedPolicy  MissedRunPolicy
	maxLookback   time.Duration
	maxCatchUp    int
	busy          sync.Mutex

	statusMu     sync.Mutex
	queuedRun    bool
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

// Retry configures how a job is retried after failure.
//
// Attempts represents total execution attempts.
// Attempts == 1 means the job executes exactly once with 0 retries.
// The default zero-value (Attempts: 0) defaults to 5 attempts.
// Use WithNoRetry() to explicitly disable retries.
type Retry struct {
	Attempts   int
	Backoff    time.Duration
	Factor     float64
	MaxBackoff time.Duration
	Jitter     bool
	Timeout    time.Duration
	Retryable  func(error) bool
}

// WithTimeout bounds a single execution of the job.
func WithTimeout(d time.Duration) JobOption {
	return func(j *Job) { j.retry.Timeout = d }
}

// WithRetry overrides the default retry behaviour for a job.
func WithRetry(r Retry) JobOption {
	return func(j *Job) {
		j.retry = executor.Retry{
			Attempts:   r.Attempts,
			Backoff:    r.Backoff,
			Factor:     r.Factor,
			MaxBackoff: r.MaxBackoff,
			Jitter:     r.Jitter,
			Timeout:    r.Timeout,
			Retryable:  r.Retryable,
		}
	}
}

// WithNoRetry configures the job to execute at most once without retries on failure.
func WithNoRetry() JobOption {
	return WithRetry(Retry{Attempts: 1})
}

// WithNoOverlap suppresses firing a job while its previous run is still active.
func WithNoOverlap() JobOption {
	return func(j *Job) {
		j.overlapPolicy = OverlapSkip
		j.overlap = false
	}
}

// WithOverlapPolicy sets the overlap policy for a job.
func WithOverlapPolicy(p OverlapPolicy) JobOption {
	return func(j *Job) {
		j.overlapPolicy = p
		j.overlap = (p == OverlapAllow)
	}
}

// WithMissedRunPolicy sets the missed-run policy for a job.
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

// WithSinceLastSuccess schedules the next fire time relative to the completion of the last successful execution.
func WithSinceLastSuccess(d time.Duration) JobOption {
	return func(j *Job) {
		j.sched = clock.SinceSuccessSchedule{Interval: d}
		j.spec = "@since_success " + d.String()
	}
}
