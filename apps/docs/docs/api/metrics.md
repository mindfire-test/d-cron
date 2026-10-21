---
id: metrics
title: Metrics & Telemetry API
sidebar_label: Metrics API
---

# 📊 Metrics & Telemetry API Reference

API reference for the observability seam in `github.com/mindfire-test/d-cron/metrics` and tracing in `github.com/mindfire-test/d-cron/otel`.

---

## 📈 `metrics.Recorder` Interface

`d-cron` defines a clean `Recorder` interface that emits scheduler lifecycle and execution metrics into whatever metrics library your application uses (e.g. Prometheus, StatsD, Datadog), without forcing heavy external dependencies into the core scheduler:

```go
type Recorder interface {
    // SetLeader records gauge flip (1 when replica is leader, 0 when standby)
    SetLeader(instance string, isLeader bool)

    // LeaderTransition counts leadership promotions/demotions
    LeaderTransition(instance string)

    // JobStarted records that a job has begun running
    JobStarted(job string)

    // JobFinished records outcome, latency, and success status
    JobFinished(job string, outcome Outcome, duration time.Duration, success bool)

    // FencedWrite records rejected writes from demoted leaders (split-brain detector)
    FencedWrite()

    // MissedRun records skipped or caught-up executions
    MissedRun(job string)
}
```

### Standard Metric Keys

| Constant | Metric Key String | Type | Description |
| :--- | :--- | :--- | :--- |
| `KeyIsLeader` | `dcron_is_leader` | Gauge | `1` if node holds leadership lock, `0` if standby |
| `KeyLeaderTransitions` | `dcron_leader_transitions_total` | Counter | Number of leadership transitions |
| `KeyJobExecutions` | `dcron_job_executions_total` | Counter | Total job runs partitioned by status |
| `KeyJobDuration` | `dcron_job_duration_seconds` | Histogram | Execution latency in seconds |
| `KeyJobLastSuccess` | `dcron_job_last_success_timestamp` | Gauge | Timestamp of last successful execution |
| `KeyJobsRunning` | `dcron_jobs_running` | Gauge | Currently running job goroutines |
| `KeyFencedWrites` | `dcron_fenced_writes_total` | Counter | Total writes rejected by epoch fence |
| `KeyMissedRuns` | `dcron_missed_runs_total` | Counter | Total missed executions skipped or caught up |

---

## 🏷️ `metrics.Outcome` Enum

Terminal execution statuses emitted to `JobFinished`:

```go
type Outcome uint8

const (
    OutcomeUnknown Outcome = iota
    OutcomeOK       // "success"
    OutcomeFailed   // "failed"
    OutcomePanicked // "panicked"
    OutcomeTimedOut // "timeout"
    OutcomeCanceled // "canceled"
    OutcomeSkipped  // "skipped"
)
```

### `metrics.Noop`
Default zero-overhead recorder that discards all signals when no metrics sink is attached.

---

## 🔍 `otel.Tracer` Interface

The `github.com/mindfire-test/d-cron/otel` package defines the tracing contract for job spans:

```go
type Tracer interface {
    StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(outcome string, err error))
}
```

### `otel.NoopTracer`
Default tracer returning the unchanged context and a no-op span completion callback.
