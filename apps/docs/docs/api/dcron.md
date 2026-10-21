---
id: dcron
title: Package dcron API Reference
sidebar_label: dcron API
---

# 📚 `dcron` Package API Reference

The primary package `github.com/mindfire-test/d-cron/dcron` contains the `Scheduler` type, constructor options, job registration methods, and fencing utilities.

---

## 🏗️ `dcron.New`

```go
func New(db *sql.DB, opts ...Option) (*Scheduler, error)
```

Constructs a new distributed `Scheduler` bound to a PostgreSQL database handle.

### Parameters
- `db`: Open `*sql.DB` connection handle.
- `opts`: Variadic list of `Option` functions.

---

## ⚙️ Configuration Options (`Option`)

### `WithNamespace(namespace string) Option`
Sets the unique cluster namespace. Resolves deterministically to a 64-bit advisory lock key (`int64`). Default: `"default"`.

### `WithLocation(loc *time.Location) Option`
Sets the time zone used to evaluate cron schedules. Default: `time.UTC`.

### `WithPollInterval(d time.Duration) Option`
Sets how often standby nodes attempt to acquire the leadership lock (with randomized jitter). Default: `5s`.

### `WithDrainTimeout(d time.Duration) Option`
Bounds how long `Stop` waits for in-flight jobs to complete before cancelling their contexts. Default: `30s`.

### `WithLogger(l *slog.Logger) Option`
Sets the structured logger used for scheduler diagnostics. Default: `slog.Default()`.

### `WithSessionStableConnection() Option`
Asserts that the connection supplied to `New` is session-stable (direct Postgres connection or session-mode pooler).

### `WithDedicatedLockConn(open func(ctx context.Context) (*sql.Conn, error)) Option`
Supplies a custom opener function that returns a dedicated, session-stable connection for the leadership lock.

### `WithDedicatedLockDSN(dsn string) Option`
Configures `d-cron` to open an independent dedicated connection to `dsn` for the lock, bypassing shared pool limits.

### `WithDedicatedLockDriver(driverName, dsn string) Option`
Same as `WithDedicatedLockDSN`, but specifies a custom registered SQL driver name.

### `WithSecondsField() Option`
Enables 6-field cron expressions with second-level precision.

### `WithMetrics(rec metrics.Recorder) Option`
Wires an observability recorder to receive leadership, execution, latency, and panic metrics.

### `WithHistory(retention time.Duration) Option`
Enables durable execution history persistence in PostgreSQL and sets the retention duration for automatic pruning.

### `WithSchema(schema string) Option`
Sets the PostgreSQL schema used for history and epoch tables. Default: `"dcron"`.

### `WithHooks(hooks ...Hook) Option`
Registers one or more outcome hooks (e.g. `WebhookHook` or custom `HookFunc`).

---

## ⚙️ Methods on `Scheduler`

### `Add(name, spec string, fn JobFunc, opts ...JobOption) error`
Registers a scheduled job. Returns error if already started, spec is invalid, or job name exists.

### `AddOnce(name string, at time.Time, fn JobFunc, opts ...JobOption) error`
Registers a one-shot job scheduled to run at a specific timestamp.

### `AddDynamic(name, spec string, fn JobFunc, opts ...JobOption) error`
Registers a new job at runtime while the scheduler is already running.

### `Pause(name string) error`
Pauses future dispatches of the named job.

### `Resume(name string) error`
Resumes dispatches of a previously paused job.

### `Remove(name string) error`
Unregisters a job from the scheduler at runtime.

### `Start(ctx context.Context) error`
Starts the leader election coordinator and scheduling loop in the background.

### `Stop(ctx context.Context) error`
Gracefully stops the scheduler, releases the advisory lock, drains running jobs within the drain timeout, and closes resources.

### `Leadership() LeadershipState`
Reports current election state: `LeadershipLeader`, `LeadershipStandby`, or `LeadershipUnknown`.

### `HealthCheck(ctx context.Context) error`
Pings the underlying database to verify connection health.

### `Jobs() []JobStatus`
Returns a point-in-time status snapshot of all registered jobs.

### `Key() int64`
Returns the resolved 64-bit PostgreSQL advisory lock key.

### `InstanceID() string`
Returns this scheduler node's unique identifier.

### `Namespace() string`
Returns the cluster namespace.

---

## 🛡️ Split-Brain Fencing Functions

### `Epoch(ctx context.Context) int64`
Extracts the active leader epoch number from a job's `context.Context`. Returns `0` if not set.

### `IdempotencyKey(ctx context.Context) string`
Extracts the execution idempotency key (format: `<job>:<epoch>:<timestamp>`).

### `NamespaceKey(ctx context.Context) string`
Extracts the namespace string carried in the job context.

### `Fence(ctx context.Context, tx Querier) error`
Validates that the leader epoch carried by `ctx` matches `dcron.leader_epoch` in PostgreSQL under `FOR SHARE` lock. Rejects stale writes from demoted leaders.

### `FenceSchema(ctx context.Context, tx Querier, schema string) error`
Same as `Fence`, with a custom database schema name.

---

## 🔔 Notification Hooks

```go
type Hook interface {
    Fire(ctx context.Context, res executor.Result) error
}

type WebhookHook struct {
    URL     string
    Timeout time.Duration
    Client  *http.Client
    Headers map[string]string
}
```

### `AdminHandler(auth AuthFunc, s *Scheduler) http.Handler`
Exposes an HTTP administration API providing `GET /status`, `POST /jobs/{name}/pause`, `POST /jobs/{name}/resume`, and `DELETE /jobs/{name}`.
