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
	sched   clock.Schedule
	fn      JobFunc
	retry   executor.Retry
	overlap bool
	busy    sync.Mutex
}

// Name returns the job's unique identifier.
func (j *Job) Name() string { return j.name }

// JobOption configures an individual job at registration time.
type JobOption func(*Job)

// WithTimeout bounds a single execution of the job. The deadline is honoured
// by cancelling the job's context; a job that ignores its context runs on.
func WithTimeout(d time.Duration) JobOption {
	return func(j *Job) { j.retry.Timeout = d }
}

// WithNoOverlap suppresses firing a job while its previous run is still
// active. Overlap is allowed by default.
func WithNoOverlap() JobOption {
	return func(j *Job) { j.overlap = false }
}
