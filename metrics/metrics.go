// Package metrics exposes observability counters and gauges for scheduler and
// job behaviour (SDS §11, issue #36).
//
// It lives in its own package so applications that do not use it do not link
// any metrics SDK (SDS NFR-402) — the core scheduler never imports this
// package. Instead, the core emits metrics through the narrow Recorder
// interface below, which an application wires in with dcron.WithMetrics. A
// Prometheus adapter can be built on top of Recorder with the Prometheus
// client library of the application's choosing; this package itself is
// dependency-free so the "no new infrastructure" pitch holds.
package metrics

import "time"

// Keys are the documented metric names (SDS §11, issue #36). They are exposed
// here so a Prometheus adapter (or any external recorder) maps them to its own
// registry with a stable contract.
const (
	KeyIsLeader          = "dcron_is_leader"
	KeyLeaderTransitions = "dcron_leader_transitions_total"
	KeyJobExecutions     = "dcron_job_executions_total"
	KeyJobDuration       = "dcron_job_duration_seconds"
	KeyJobLastSuccess    = "dcron_job_last_success_timestamp"
	KeyJobsRunning       = "dcron_jobs_running"
	KeyFencedWrites      = "dcron_fenced_writes_total"
	KeyMissedRuns        = "dcron_missed_runs_total"
)

// Outcome is the terminal result of one logical execution. Its String() maps
// onto the status dimension of dcron_job_executions_total and the dashboard.
type Outcome uint8

const (
	OutcomeUnknown Outcome = iota
	OutcomeOK
	OutcomeFailed
	OutcomePanicked
	OutcomeTimedOut
	OutcomeCanceled
	OutcomeSkipped
)

// String returns the metric/label-safe form of o.
func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "success"
	case OutcomeFailed:
		return "failed"
	case OutcomePanicked:
		return "panicked"
	case OutcomeTimedOut:
		return "timeout"
	case OutcomeCanceled:
		return "canceled"
	case OutcomeSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// Recorder is the seam through which the core emits observability signals. An
// application supplies a Recorder via dcron.WithMetrics; the implementation
// reports into whatever registry the app owns (SDS §11, issue #36 / FR-404).
// Implementations must be safe for concurrent use.
type Recorder interface {
	// SetLeader records a gauge flip: isLeader is 1 when this replica holds
	// leadership, 0 otherwise (dcron_is_leader).
	SetLeader(instance string, isLeader bool)
	// LeaderTransition counts a membership transition (flapping detector).
	LeaderTransition(instance string)
	// JobStarted records that a job has begun running (dcron_jobs_running).
	JobStarted(job string)
	// JobFinished records a terminal outcome, duration in ms, and whether it
	// was a success (drives dcron_job_executions_total, dcron_job_duration_seconds,
	// dcron_job_last_success_timestamp, and dcron_jobs_running).
	JobFinished(job string, outcome Outcome, duration time.Duration, success bool)
	// FencedWrite records a rejected write from a demoted leader
	// (dcron_fenced_writes_total). Non-zero is a genuine split-brain signal.
	FencedWrite()
	// MissedRun records a skipped/catch-up fire (dcron_missed_runs_total).
	MissedRun(job string)
}

// Noop is the default Recorder that discards everything; it keeps callers
// safe when no metrics sink is configured.
type Noop struct{}

// SetLeader implements Recorder.
func (Noop) SetLeader(_ string, _ bool) {}

// LeaderTransition implements Recorder.
func (Noop) LeaderTransition(_ string) {}

// JobStarted implements Recorder.
func (Noop) JobStarted(_ string) {}

// JobFinished implements Recorder.
func (Noop) JobFinished(_ string, _ Outcome, _ time.Duration, _ bool) {}

// FencedWrite implements Recorder.
func (Noop) FencedWrite() {}

// MissedRun implements Recorder.
func (Noop) MissedRun(_ string) {}

var _ Recorder = (*Noop)(nil)
