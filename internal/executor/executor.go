// Package executor runs jobs with panic recovery, timeout, retry, overlap
// control, and bounded drain.
//
// A job function is a plain func(context.Context) error; the executor never
// inspects its payload or serialisation (see the SDS). See SDS §5 and §7 for
// the execution and failure-handling semantics.
package executor

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Func is the contract for a schedulable job.
type Func func(ctx context.Context) error

// Outcome classifies a single logical execution (after retries).
type Outcome uint8

const (
	OutcomeUnknown Outcome = iota
	OutcomeOK
	// OutcomeFailed is a non-retryable error, or the last error once the
	// retry policy was exhausted.
	OutcomeFailed
	// OutcomePanicked is a recovered panic. Panics are never retried: they
	// almost always indicate a programming bug, and retrying can mask it.
	OutcomePanicked
	OutcomeTimedOut
	OutcomeCanceled
)

// String returns a short, log-friendly label for the outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeFailed:
		return "failed"
	case OutcomePanicked:
		return "panicked"
	case OutcomeTimedOut:
		return "timed_out"
	case OutcomeCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

// Retry configures how Run retries a job after failures.
type Retry struct {
	// Attempts is the total number of runs (>=1). 1 disables retry.
	Attempts int
	// Backoff is the delay after the first failure before the first retry.
	Backoff time.Duration
	// Factor multiplies the backoff between retries. Values >1 yield
	// exponential backoff; 1 (the default) is constant backoff.
	Factor float64
	// MaxBackoff caps the backoff. 0 = unlimited.
	MaxBackoff time.Duration
	// Jitter adds up to +/-25% jitter to each backoff to avoid thundering
	// herds across replicas.
	Jitter bool
	// Timeout is the per-attempt deadline. 0 = inherit the caller's context.
	Timeout time.Duration
	// Retryable reports whether a returned error is worth retrying. By
	// default every error is retried (subject to Attempts).
	Retryable func(error) bool
}

func (r Retry) withDefaults() Retry {
	if r.Attempts < 1 {
		r.Attempts = 5
	}
	if r.Backoff <= 0 {
		r.Backoff = time.Second
	}
	if r.Factor <= 0 {
		r.Factor = 2
	}
	if r.MaxBackoff <= 0 {
		r.MaxBackoff = 5 * time.Minute
	}

	if r.Timeout <= 0 {
		r.Timeout = 30 * time.Minute
	}
	if r.Retryable == nil {
		r.Retryable = func(error) bool { return true }
	}
	return r
}

// Delay returns the delay to wait before the next retry. attempt is the
// 1-indexed number of failures seen so far. Exported so callers and tests can
// predict retry timing.
func (r Retry) Delay(attempt int) time.Duration {
	d := r.Backoff
	if r.Factor > 1 {
		d = time.Duration(float64(d) * math.Pow(r.Factor, float64(attempt)))
	}
	if r.MaxBackoff > 0 && d > r.MaxBackoff {
		d = r.MaxBackoff
	}
	if r.Jitter && d > 0 {
		delta := d / 4
		if delta > 0 {
			d = time.Duration(int64(d) - int64(delta) + rand.Int64N(2*int64(delta)))
		}
	}
	return d
}

// Result is the outcome of a logical execution.
type Result struct {
	// Name is the job name passed to Run (issue #36).
	Name string
	// Outcome classifies the terminal state after all retries.
	Outcome Outcome
	// Error is the terminal error (nil on success). It may be a *PanicError
	// or *TimeoutError carrying the job name (issue #26).
	Error error
	// Attempts is the total number of run attempts made (>= 1).
	Attempts int
	// Duration is the wall-clock time from the first attempt start to completion
	// of the logical execution, including inter-attempt backoff. It is the value
	// surfaced to metrics and dashboards (issue #36).
	Duration time.Duration
}

// PanicError is the typed result of a recovered panic. It carries the panic
// value and the full goroutine stack captured inside the deferred recovery, so
// a panicking job never crashes the host process and the stack is not lost
// (SDS §5.1, issue #18). The limitation — a panic on a goroutine the job
// itself spawned cannot be recovered — is documented on the Run function.
type PanicError struct {
	// Job is the name of the scheduled job that panicked, or "" when Run was
	// invoked without a name (issue #26).
	Job   string
	Value any
	Stack []byte
}

// Error implements error. The job name is included so an operator can jump
// straight to the culprit without cross-referencing log lines (issue #26).
func (e *PanicError) Error() string {
	if e.Job != "" {
		return fmt.Sprintf("executor: recovered panic in job %q: %v", e.Job, e.Value)
	}
	return fmt.Sprintf("executor: recovered panic: %v", e.Value)
}

// StackTrace returns the goroutine stack captured at panic time.
func (e *PanicError) StackTrace() []byte { return e.Stack }

// TimeoutError is the typed result of a job that exceeded its per-attempt
// deadline (SDS §5.2, issue #19/#26). It wraps context.DeadlineExceeded so it
// remains errors.Is-friendly, and carries the job name for diagnostics.
type TimeoutError struct {
	Job string
	Err error
}

// Error implements error, naming the affected job when available (issue #26).
func (e *TimeoutError) Error() string {
	if e.Job != "" {
		return fmt.Sprintf("executor: job %q timed out: %v", e.Job, e.Err)
	}
	return fmt.Sprintf("executor: timed out: %v", e.Err)
}

// Unwrap lets callers errors.Is/As the underlying cause (issue #26).
func (e *TimeoutError) Unwrap() error { return e.Err }
