---
id: dcron
title: API Reference
sidebar_label: API Reference
---

# 📚 Go API Reference

Complete API reference for `d-cron` core packages: `dcron`, `executor`, `metrics`, and `otel`.

---

## 🏗️ Package `dcron`

The primary package `github.com/mindfire-test/d-cron/dcron` contains the `Scheduler` type, constructor options, job registration methods, and fencing utilities.

### `dcron.New`

```go
func New(db *sql.DB, opts ...Option) (*Scheduler, error)
```

Constructs a new distributed `Scheduler` bound to a PostgreSQL database handle.

### Configuration Options (`dcron.Option`)

| Option | Signature | Description |
| :--- | :--- | :--- |
| `WithNamespace` | `WithNamespace(namespace string) Option` | Sets cluster namespace; derives 64-bit advisory lock key. Default: `"default"`. |
| `WithLocation` | `WithLocation(loc *time.Location) Option` | Sets timezone for cron evaluation. Default: `time.UTC`. |
| `WithPollInterval` | `WithPollInterval(d time.Duration) Option` | Standby lock polling interval (with jitter). Default: `5s`. |
| `WithDrainTimeout` | `WithDrainTimeout(d time.Duration) Option` | Grace period on `Stop` before cancelling jobs. Default: `30s`. |
| `WithLogger` | `WithLogger(l *slog.Logger) Option` | Sets structured logger for scheduler diagnostics. Default: `slog.Default()`. |
| `WithSessionStableConnection` | `WithSessionStableConnection() Option` | Asserts DB handle is session-stable (direct Postgres or session-mode pooler). |
| `WithDedicatedLockDSN` | `WithDedicatedLockDSN(dsn string) Option` | Opens dedicated direct connection for advisory locks, bypassing shared pool. |
| `WithDedicatedLockConn` | `WithDedicatedLockConn(open func(ctx context.Context) (*sql.Conn, error)) Option` | Custom opener returning dedicated connection for lock. |
| `WithSecondsField` | `WithSecondsField() Option` | Enables 6-field cron expressions with second-level precision. |
| `WithMetrics` | `WithMetrics(rec metrics.Recorder) Option` | Wires an observability recorder to receive lifecycle and execution metrics. |
| `WithHistory` | `WithHistory(retention time.Duration) Option` | Enables durable execution history in PostgreSQL with automatic pruning. |
| `WithSchema` | `WithSchema(schema string) Option` | Sets PostgreSQL schema used for history and epoch tables. Default: `"dcron"`. |
| `WithHooks` | `WithHooks(hooks ...Hook) Option` | Registers notification hooks (e.g. `WebhookHook`). |

---

### Methods on `Scheduler`

```go
// Registration
func (s *Scheduler) Add(name, spec string, fn JobFunc, opts ...JobOption) error
func (s *Scheduler) AddOnce(name string, at time.Time, fn JobFunc, opts ...JobOption) error
func (s *Scheduler) AddDynamic(name, spec string, fn JobFunc, opts ...JobOption) error

// Job Controls
func (s *Scheduler) Pause(name string) error
func (s *Scheduler) Resume(name string) error
func (s *Scheduler) Remove(name string) error

// Lifecycle
func (s *Scheduler) Start(ctx context.Context) error
func (s *Scheduler) Stop() error
func (s *Scheduler) Leadership() LeadershipState
func (s *Scheduler) HealthCheck(ctx context.Context) error

// Introspection
func (s *Scheduler) Jobs() []JobStatus
func (s *Scheduler) Key() int64
func (s *Scheduler) InstanceID() string
func (s *Scheduler) Namespace() string
```

---

### Fencing Functions

```go
// Read fencing metadata from job context
func Epoch(ctx context.Context) int64
func IdempotencyKey(ctx context.Context) string
func NamespaceKey(ctx context.Context) string

// Verify epoch in PostgreSQL transaction under FOR SHARE lock
func Fence(ctx context.Context, tx Querier) error
func FenceSchema(ctx context.Context, tx Querier, schema string) error
```

---

## 🛠️ Package `executor`: Job Options & Policies

Job functions registered with `d-cron` implement the standard signature:

```go
type JobFunc func(ctx context.Context) error
```

### Job Configuration Options (`JobOption`)

| Option | Signature | Description |
| :--- | :--- | :--- |
| `WithTimeout` | `WithTimeout(d time.Duration) JobOption` | Sets maximum execution duration per run. |
| `WithRetry` | `WithRetry(r Retry) JobOption` | Configures exponential backoff retry behavior. |
| `WithNoOverlap` | `WithNoOverlap() JobOption` | Shorthand for `WithOverlapPolicy(OverlapSkip)`. |
| `WithOverlapPolicy` | `WithOverlapPolicy(p OverlapPolicy) JobOption` | `OverlapSkip`, `OverlapQueue`, or `OverlapAllow`. |
| `WithMissedRunPolicy` | `WithMissedRunPolicy(p MissedRunPolicy) JobOption` | `MissedSkip` or `MissedCatchUp`. |
| `WithMaxLookback` | `WithMaxLookback(d time.Duration) JobOption` | Bounds catch-up window for missed runs. |
| `WithMaxCatchUpRuns` | `WithMaxCatchUpRuns(n int) JobOption` | Caps maximum executions dispatched during catch-up. |

### Retry Configuration Struct

```go
type Retry struct {
    Attempts   int           // Total execution attempts (e.g. 3)
    Backoff    time.Duration // Initial retry delay (e.g. 500ms)
    Factor     float64       // Multiplier (e.g. 2.0)
    MaxBackoff time.Duration // Maximum delay cap (e.g. 10s)
    Jitter     bool          // Add randomized jitter
    Timeout    time.Duration // Per-attempt timeout
}
```

---

## 📈 Package `metrics`: Observability Seam

```go
type Recorder interface {
    SetLeader(instance string, isLeader bool)
    LeaderTransition(instance string)
    JobStarted(job string)
    JobFinished(job string, outcome Outcome, duration time.Duration, success bool)
    FencedWrite()
    MissedRun(job string)
}
```

### Execution Outcomes (`metrics.Outcome`)

```go
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

---

## 🔍 Package `otel`: Distributed Tracing

```go
type Tracer interface {
    StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(outcome string, err error))
}
```
Emitted spans capture `job.name`, `attempt`, and the `leader.epoch` fencing token.
