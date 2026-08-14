package dcron

import "context"

// JobFunc is the signature of a job's execution function. It receives only a
// context and returns only an error, which keeps the scheduler free of
// payloads and serialisation (see the SDS).
type JobFunc func(ctx context.Context) error

// Job is a registered, schedulable unit of work.
//
// Jobs are created through (Scheduler).Add, which lands in Phase 1, and are
// not intended to be constructed directly.
type Job struct {
	name string
}

// Name returns the job's unique identifier.
func (j *Job) Name() string {
	return j.name
}

// JobOption configures an individual job at registration time. The option
// constructors (for example WithTimeout) land with job registration in
// Phase 1.
type JobOption func(*Job)
