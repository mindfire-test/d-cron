---
id: dcron
title: Package dcron API Reference
sidebar_label: dcron API
---

# 📚 `dcron` Package API Reference

The primary package `github.com/mindfiredigital/d-cron/dcron` contains the `Scheduler` type and builder configuration functions.

---

## 🏗️ `dcron.New`

```go
func New(db *sql.DB, opts ...Option) (*Scheduler, error)
```

Constructs a new `Scheduler` instance connected to PostgreSQL.

### Parameters
- `db`: Open `*sql.DB` connection handle.
- `opts`: Variadic list of `Option` functions.

---

## ⚙️ Configuration Options (`Option`)

### `WithNamespace(namespace string) Option`
Sets the unique cluster namespace. Resolves to a deterministic 64-bit advisory lock key. Default: `"dcron-default"`.

### `WithInstanceID(id string) Option`
Sets a human-readable instance identifier for leader metadata and telemetry. Default: Hostname.

### `WithPollInterval(d time.Duration) Option`
Sets the interval at which Standby instances attempt to acquire leadership lock. Default: `3s`.

### `WithHeartbeatInterval(d time.Duration) Option`
Sets the interval at which the Leader verifies session health. Default: `1s`.

### `WithHistoryStore(store HistoryStore) Option`
Attaches an audit store to record job executions.

### `WithMetrics(m MetricsExporter) Option`
Attaches a metrics collector (e.g. Prometheus).

### `WithTracer(t Tracer) Option`
Attaches an OpenTelemetry tracer provider.

### `WithPanicHandler(fn PanicHandlerFunc) Option`
Configures a custom callback for caught job panics.

---

## ⚙️ Methods on `Scheduler`

### `Add(name string, schedule string, fn JobFunc) error`
Registers a new scheduled job with default options (`OverlapSkip`, `MissedRunSkip`).

### `AddWithOptions(name string, schedule string, fn JobFunc, opts JobOptions) error`
Registers a job with explicit overlap, missed run, and timeout policies.

### `Start(ctx context.Context) error`
Starts the non-blocking background leader election and clock engine.

### `Stop()`
Gracefully stops the clock engine and releases advisory locks.

### `IsLeader() bool`
Returns `true` if the local instance currently holds active leadership.

---

## 🔑 Helper Functions

### `GetFencingToken(ctx context.Context) (FencingToken, bool)`
Extracts the active `FencingToken` (containing `Epoch` and `LeaderID`) from a job handler's `context.Context`.
