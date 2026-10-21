---
id: usage-guide
title: Usage Guide
sidebar_label: Usage Guide
slug: /usage-guide
---

# Usage Guide

This guide covers system requirements, module installation, database configuration, and core usage patterns for d-cron.

---

## Requirements

| Requirement | Minimum Version | Recommended | Notes |
| :--- | :--- | :--- | :--- |
| **Go** | 1.21+ | 1.23+ | Uses standard library `context`, `sync`, and `log/slog` |
| **PostgreSQL** | 12.0+ | 14.0+ | Supports 64-bit Session Advisory Locks (`pg_try_advisory_lock`) |
| **Go Database Driver** | `database/sql` (`lib/pq` or `pgx/v5`) | `pgx/v5` (stdlib mode) or `lib/pq` | Any standard `*sql.DB` connection pool |

---

## Module Installation

Install d-cron using standard Go modules:

```bash
go get github.com/mindfire-test/d-cron@latest
```

All optional sub-packages (metrics, tracing, web dashboard UI) reside in the same repository:

```go
import (
    "github.com/mindfire-test/d-cron/dcron"
    "github.com/mindfire-test/d-cron/metrics"
    "github.com/mindfire-test/d-cron/otel"
    "github.com/mindfire-test/d-cron/ui"
)
```

---

## Database Setup & Permissions

### Zero-Migration Default Mode

By default, d-cron operates using **PostgreSQL Session Advisory Locks**:
- It requires **zero schema migrations**.
- It creates **zero database tables**.
- It requires no special DDL (`CREATE TABLE`) privileges.

All d-cron requires is a standard PostgreSQL role with `CONNECT` permissions on your existing application database:

```sql
GRANT CONNECT ON DATABASE myapp_db TO app_user;
```

### Optional History & Audit Store Setup

If you enable durable execution history logging (`dcron.WithHistory(retention)`), d-cron automatically creates and migrates its required schema (default `"dcron"`) and tables (`leader_epoch` and `execution`) under a serialized advisory lock.

If your database user does not have DDL privileges, you can execute the idempotent DDL migration script manually:

```sql
CREATE SCHEMA IF NOT EXISTS dcron;

CREATE TABLE IF NOT EXISTS dcron.leader_epoch (
    namespace   TEXT PRIMARY KEY,
    epoch       BIGINT NOT NULL,
    instance_id TEXT NOT NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dcron.execution (
    id            BIGSERIAL PRIMARY KEY,
    namespace     TEXT NOT NULL,
    job_name      TEXT NOT NULL,
    scheduled_at  TIMESTAMPTZ NOT NULL,
    started_at    TIMESTAMPTZ NOT NULL,
    finished_at   TIMESTAMPTZ,
    duration_ms   BIGINT,
    outcome       TEXT NOT NULL,
    error         TEXT,
    attempt       INT NOT NULL DEFAULT 1,
    leader_epoch  BIGINT NOT NULL,
    instance_id   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_dcron_exec_job_sched 
    ON dcron.execution(namespace, job_name, scheduled_at DESC);
```

---

## Scheduler Configuration & Lifecycle

### 1. Constructing the Scheduler

When initializing `dcron.New`, you can pass functional options to tailor behavior:

```go
scheduler, err := dcron.New(db,
    // Unique namespace for advisory lock isolation
    dcron.WithNamespace("billing-service"),

    // Frequency with which standbys check for leader promotion
    dcron.WithPollInterval(3*time.Second),

    // Ensures the election uses a dedicated, stable PostgreSQL session connection
    dcron.WithSessionStableConnection(),

    // Structured logging via standard log/slog
    dcron.WithLogger(slog.Default()),

    // Optional: Maximum execution timeout applied to all jobs
    dcron.WithTimeout(15*time.Minute),
)
```

### 2. Registering Jobs

Register jobs with a name, a cron expression (5-field or macros like `@daily`), and a handler function:

```go
err = scheduler.Add("cache-sync", "@every 10m", func(ctx context.Context) error {
    epoch := dcron.Epoch(ctx)
    slog.Info("Running cache sync", "epoch", epoch)
    return syncCache(ctx)
})
```

### 3. Starting the Scheduler

Start the background leader election and min-heap timer clock:

```go
if err := scheduler.Start(context.Background()); err != nil {
    log.Fatalf("Failed to start scheduler: %v", err)
}
```

### 4. Graceful Shutdown

Always shut down the scheduler gracefully upon receiving OS termination signals (`SIGINT`, `SIGTERM`):

```go
stopChan := make(chan os.Signal, 1)
signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
<-stopChan

// Waits for currently executing in-flight jobs to finish cleanly and releases the lock
if err := scheduler.Stop(); err != nil {
    log.Printf("Scheduler shutdown warning: %v", err)
}
```

---

## Running CLI Examples

The repository includes ready-to-run examples in the `examples/` directory:

### Minimal Standalone Runner
```bash
go run ./examples/minimal/main.go -db "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable"
```

### Dashboard & Metrics Web Runner
```bash
go run ./examples/with-dashboard/main.go -port 8080
```
Open `http://localhost:8080/dcron/ui/` in your browser to view the embedded dashboard.

---

## Next Steps

- Explore [Core Architecture](./architecture/advisory-locks.md) to understand PostgreSQL advisory locks and leader election.
- Review [Execution Safety & Retries](./guides/execution-safety.md) for panic recovery and timeout configurations.
