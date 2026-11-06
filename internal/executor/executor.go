// Package executor runs jobs with panic recovery, timeout, retry, and bounded drain.
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
	OutcomeFailed
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
	Attempts   int
	Backoff    time.Duration
	Factor     float64
	MaxBackoff time.Duration
	Jitter     bool
	Timeout    time.Duration
	Retryable  func(error) bool
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

// Delay returns the delay to wait before the next retry.
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
	Name     string
	Outcome  Outcome
	Error    error
	Attempts int
	Duration time.Duration
}

// PanicError is the typed result of a recovered panic.
type PanicError struct {
	Job   string
	Value any
	Stack []byte
}

// Error implements error.
func (e *PanicError) Error() string {
	if e.Job != "" {
		return fmt.Sprintf("executor: recovered panic in job %q: %v", e.Job, e.Value)
	}
	return fmt.Sprintf("executor: recovered panic: %v", e.Value)
}

// StackTrace returns the goroutine stack captured at panic time.
func (e *PanicError) StackTrace() []byte { return e.Stack }

// TimeoutError is the typed result of a job that exceeded its deadline.
type TimeoutError struct {
	Job string
	Err error
}

// Error implements error.
func (e *TimeoutError) Error() string {
	if e.Job != "" {
		return fmt.Sprintf("executor: job %q timed out: %v", e.Job, e.Err)
	}
	return fmt.Sprintf("executor: timed out: %v", e.Err)
}

// Unwrap lets callers unwrap the underlying cause.
func (e *TimeoutError) Unwrap() error { return e.Err }
