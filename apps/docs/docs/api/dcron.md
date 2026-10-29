---
id: dcron
title: API Reference
sidebar_label: API Reference
---

# Go API Reference

Complete API reference for `d-cron` core packages: `dcron`, `executor`, `metrics`, and `otel`.

## Package `dcron`

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
| `WithPollInterval` | `WithPollInterval(d time.Duration) Option` | Standby lock polling interval (with ±20% jitter). Default: `5s`. |
| `WithDrainTimeout` | `WithDrainTimeout(d time.Duration) Option` | Grace period on `Stop` before cancelling in-flight jobs. Default: `30s`. |
| `WithStartupTimeout` | `WithStartupTimeout(d time.Duration) Option` | Bounds initial DB operations (lock connection, migrations, keepalives). Default: `15s`. |
| `WithPollTimeout` | `WithPollTimeout(d time.Duration) Option` | Per-attempt timeout for advisory lock probe queries. Default: `5s`. |
| `WithDBTimeout` | `WithDBTimeout(d time.Duration) Option` | Per-operation timeout for history store and admin DB queries. Default: `5s`. |
| `WithOpTimeout` | `WithOpTimeout(d time.Duration) Option` | Alias for `WithDBTimeout`. Default: `5s`. |
| `WithLogger` | `WithLogger(l *slog.Logger) Option` | Sets structured logger for scheduler diagnostics. Default: `slog.Default()`. |
| `WithSessionStableConnection` | `WithSessionStableConnection() Option` | Asserts DB handle is session-stable (direct Postgres or session-mode pooler). |
| `WithDedicatedLockDSN` | `WithDedicatedLockDSN(dsn string) Option` | Opens dedicated direct Postgres connection for advisory locks, bypassing shared pool. |
| `WithDedicatedLockDriver` | `WithDedicatedLockDriver(driverName, dsn string) Option` | Opens dedicated direct connection with custom registered driver (e.g. `"pgx"`). |
| `WithDedicatedLockConn` | `WithDedicatedLockConn(open func(ctx context.Context) (*sql.Conn, error)) Option` | Custom opener returning dedicated connection for lock. |
| `WithLeaderForwardURL` | `WithLeaderForwardURL(url string) Option` | Target URL to proxy admin mutation requests from standby to active leader. |
| `WithJobHandler` | `WithJobHandler(name string, fn JobFunc) Option` | Registers named handler for restoring dynamic database jobs across failover. |
| `WithSecondsField` | `WithSecondsField() Option` | Enables 6-field cron expressions with second-level precision. |
| `WithMetrics` | `WithMetrics(rec metrics.Recorder) Option` | Wires an observability recorder to receive lifecycle and execution metrics. |
| `WithTracer` | `WithTracer(t otel.Tracer) Option` | Attaches OpenTelemetry tracer for instrumenting job executions. |
| `WithHistory` | `WithHistory(retention time.Duration) Option` | Enables durable execution history in PostgreSQL with automatic pruning. |
| `WithSchema` | `WithSchema(schema string) Option` | Sets PostgreSQL schema used for history and epoch tables. Default: `"dcron"`. |
| `WithHooks` | `WithHooks(hooks ...Hook) Option` | Registers notification hooks (e.g. `WebhookHook`). |

### Methods on `Scheduler`

```go
// Registration
func (s *Scheduler) Add(name, spec string, fn JobFunc, opts ...JobOption) error
func (s *Scheduler) AddOnce(name string, at time.Time, fn JobFunc, opts ...JobOption) error
func (s *Scheduler) AddDynamic(name, spec string, fn JobFunc, opts ...JobOption) error
func (s *Scheduler) RegisterHandler(name string, fn JobFunc)

// Job Controls (Durable in PostgreSQL, failover safe)
func (s *Scheduler) Pause(name string) error
func (s *Scheduler) Resume(name string) error
func (s *Scheduler) Remove(name string) error

// Lifecycle
func (s *Scheduler) Start(ctx context.Context) error
func (s *Scheduler) Stop(ctx context.Context) error
func (s *Scheduler) Leadership() LeadershipState
func (s *Scheduler) HealthCheck(ctx context.Context) error

// Introspection
func (s *Scheduler) Jobs() []JobStatus
func (s *Scheduler) Epoch() int64
func (s *Scheduler) Key() int64
func (s *Scheduler) InstanceID() string
func (s *Scheduler) Namespace() string
```

### Job Status Model

```go
type JobStatus struct {
    Name         string
    Spec         string
    NextRun      time.Time
    LastRun      time.Time
    LastOutcome  string
    LastError    string
    LastDuration time.Duration
    Running      bool
    Paused       bool
}

// JobInfo is an exported alias for JobStatus.
type JobInfo = JobStatus
```

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

### Error Sanitization

```go
// RedactError sanitizes DSN credentials, passwords, Bearer tokens,
// and API keys from error messages while preserving non-sensitive context.
func RedactError(err error) string
```

### Admin REST API & Authentication

```go
// AuthFunc validates incoming administrative requests.
type AuthFunc func(r *http.Request) bool

// RequireBearerToken returns an AuthFunc that validates against a static token
// using constant-time comparison. Empty token safely rejects all requests.
func RequireBearerToken(token string) AuthFunc

// AdminHandler exposes cluster inspection and job control REST endpoints.
// Note: If auth is nil, endpoints are unauthenticated.
func AdminHandler(auth AuthFunc, s *Scheduler) http.Handler
```

## Package `executor`: Job Options & Policies

Job functions registered with `d-cron` implement the standard signature:

```go
type JobFunc func(ctx context.Context) error
```

### Job Configuration Options (`JobOption`)

| Option | Signature | Description |
| :--- | :--- | :--- |
| `WithTimeout` | `WithTimeout(d time.Duration) JobOption` | Sets maximum execution duration per run. |
| `WithRetry` | `WithRetry(r Retry) JobOption` | Configures exponential backoff retry behavior. |
| `WithNoRetry` | `WithNoRetry() JobOption` | Configures job to run at most once without retries (`Attempts: 1`). |
| `WithNoOverlap` | `WithNoOverlap() JobOption` | Shorthand for `WithOverlapPolicy(OverlapSkip)`. |
| `WithOverlapPolicy` | `WithOverlapPolicy(p OverlapPolicy) JobOption` | `OverlapSkip`, `OverlapQueue`, or `OverlapAllow`. |
| `WithMissedRunPolicy` | `WithMissedRunPolicy(p MissedRunPolicy) JobOption` | `MissedSkip` or `MissedCatchUp`. |
| `WithMaxLookback` | `WithMaxLookback(d time.Duration) JobOption` | Bounds catch-up window for missed runs. |
| `WithMaxCatchUpRuns` | `WithMaxCatchUpRuns(n int) JobOption` | Caps maximum executions dispatched during catch-up. |
| `WithSinceLastSuccess` | `WithSinceLastSuccess(d time.Duration) JobOption` | Schedules next run relative to completion of previous successful run. |

### Retry Configuration Struct

```go
type Retry struct {
    Attempts   int               // Total execution attempts (Attempts == 1 means 0 retries)
    Backoff    time.Duration     // Initial retry delay (e.g. 500ms)
    Factor     float64           // Multiplier (e.g. 2.0)
    MaxBackoff time.Duration     // Maximum delay cap (e.g. 10s)
    Jitter     bool              // Add randomized jitter
    Timeout    time.Duration     // Per-attempt timeout
    Retryable  func(error) bool  // Optional predicate: return false to abort retries early
}
```

> **Note on Retry Defaults**: The default zero-value (`Attempts: 0`) retains backward compatibility and defaults to 5 attempts. To disable retries explicitly for a non-idempotent job, pass `dcron.WithNoRetry()` or `dcron.WithRetry(dcron.Retry{Attempts: 1})`.

### Terminal Execution Result & Hooks

```go
type Result struct {
    Name     string
    Outcome  string
    Error    error
    Attempts int
    Duration time.Duration
}

const (
    OutcomeOK       = "ok"
    OutcomeFailed   = "failed"
    OutcomePanicked = "panicked"
    OutcomeTimedOut = "timed_out"
    OutcomeCanceled = "canceled"
    OutcomeUnknown  = "unknown"
)

type Hook interface {
    Fire(ctx context.Context, res Result) error
}
```

## Package `metrics`: Observability Seam

```go
const (
    KeyIsLeader          = "dcron_is_leader"
    KeyLeaderTransitions = "dcron_leader_transitions_total"
    KeyJobExecutions     = "dcron_job_executions_total"
    KeyJobDuration       = "dcron_job_duration_seconds"
    KeyJobLastSuccess    = "dcron_job_last_success_timestamp"
    KeyJobsRunning       = "dcron_jobs_running"
    KeyFencedWrites      = "dcron_fenced_writes_total"
    KeyMissedRuns        = "dcron_missed_runs_total"
    KeyRecordFailures    = "dcron_record_failures_total"
    KeyBackendErrors     = "dcron_backend_errors_total"
)

type Recorder interface {
    SetLeader(instance string, isLeader bool)
    LeaderTransition(instance string)
    JobStarted(job string)
    JobFinished(job string, outcome Outcome, duration time.Duration, success bool)
    FencedWrite()
    MissedRun(job string)
}

type HistoryRecorder interface {
    RecordFailure(job string)
}

type BackendErrorRecorder interface {
    BackendError(operation string)
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

## Package `otel`: Distributed Tracing

```go
type Tracer interface {
    StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(outcome string, err error))
}
```
Emitted spans capture `job.name`, `attempt`, and the `leader.epoch` fencing token. Wire into `dcron.New` using `dcron.WithTracer(tracer)`.
