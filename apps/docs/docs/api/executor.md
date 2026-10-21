---
id: executor
title: Job Types & Options
sidebar_label: Job & Executor Types
---

# 🛠️ Job Types & Options Reference

Detailed reference of job handler signatures, execution options, retry policies, and outcome models in `d-cron`.

---

## 📝 `JobFunc` Signature

```go
type JobFunc func(ctx context.Context) error
```

Every scheduled job function registered with `d-cron` must match this signature. The provided `ctx` carries cancellation signals, timeouts, leader epoch tokens, and idempotency keys.

---

## ⚙️ Job Options (`JobOption`)

Job options are functional options passed to `scheduler.Add`, `scheduler.AddOnce`, or `scheduler.AddDynamic`.

### `WithTimeout(d time.Duration) JobOption`
Bounds execution of the job to duration `d`. When exceeded, `ctx.Done()` is closed with `context.DeadlineExceeded`.

### `WithRetry(r Retry) JobOption`
Configures exponential backoff retry behavior for failed job executions:

```go
type Retry struct {
    Attempts   int           // Total execution attempts (e.g. 3)
    Backoff    time.Duration // Initial retry delay (e.g. 500ms)
    Factor     float64       // Exponential multiplier (e.g. 2.0)
    MaxBackoff time.Duration // Maximum backoff cap (e.g. 10s)
    Jitter     bool          // Add randomized jitter to prevent thundering herd
    Timeout    time.Duration // Per-attempt timeout
}
```

### `WithNoOverlap() JobOption`
Shorthand for `WithOverlapPolicy(OverlapSkip)`. Prevents a job from firing while its previous instance is still running.

### `WithOverlapPolicy(p OverlapPolicy) JobOption`
Controls behavior when a scheduled run is due while the previous run is still executing:

```go
type OverlapPolicy int

const (
    OverlapSkip  OverlapPolicy = iota // Skip the new scheduled run (default)
    OverlapQueue                      // Queue one execution to run after the active run completes
    OverlapAllow                      // Allow concurrent parallel executions
)
```

### `WithMissedRunPolicy(p MissedRunPolicy) JobOption`
Governs how `d-cron` handles schedule triggers missed during cluster downtime or restarts:

```go
type MissedRunPolicy int

const (
    MissedSkip    MissedRunPolicy = iota // Skip missed triggers and wait for next scheduled time
    MissedCatchUp                        // Dispatch missed runs sequentially (requires WithHistory)
)
```

### `WithMaxLookback(d time.Duration) JobOption`
Caps how far back in time `MissedCatchUp` will search for unexecuted schedules (e.g. `24 * time.Hour`).

### `WithMaxCatchUpRuns(maxRuns int) JobOption`
Caps the maximum number of catch-up executions dispatched for a job.

### `WithSinceLastSuccess(d time.Duration) JobOption`
Schedules the next fire time relative to the completion timestamp of the last *successful* execution rather than fixed clock intervals. Requires `WithHistory`.

---

## 📊 Result & Execution Outcomes

Terminal execution results are passed to registered hooks and metrics:

```go
type Result struct {
    Name       string          // Job name
    Outcome    metrics.Outcome // Final status
    Attempts   int             // Number of attempts made
    Duration   time.Duration   // Total execution latency
    Error      error           // Returned error if failed
    PanicValue any             // Recovered value if panicked
    Stack      []byte          // Stack trace bytes if panicked
}
```
