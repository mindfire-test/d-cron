// Package executor runs jobs with panic recovery, timeout, retry, overlap
// control, and bounded drain.
//
// A job function is a plain func(context.Context) error; the executor never
// inspects its payload or serialisation (see the SDS). See SDS §5 and §7 for
// the execution and failure-handling semantics.
package executor

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// Func is the contract for a schedulable job.
type Func func(ctx context.Context) error

// Outcome classifies a single logical execution (after retries).
type Outcome uint8

const (
	OutcomeUnknown Outcome = iota // reserved for zero value
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

// withDefaults fills in sane defaults so callers may leave fields zero.
func (r Retry) withDefaults() Retry {
	if r.Attempts < 1 {
		r.Attempts = 1
	}
	if r.Factor <= 0 {
		r.Factor = 1
	}
	if r.Retryable == nil {
		r.Retryable = func(error) bool { return true }
	}
	return r
}

// backoff returns the delay to wait before the next retry. attempt is the
// 1-indexed number of failures seen so far.
func (r Retry) backoff(attempt int) time.Duration {
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
	Outcome  Outcome
	Error    error
	Attempts int
}
