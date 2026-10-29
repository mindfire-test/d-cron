---
id: usage-guide
title: Usage Guide
sidebar_label: Usage Guide
slug: /usage-guide
---

# Usage Guide

This guide explains how to configure, operate, and manage background cron jobs with d-cron in your Go applications.

For prerequisites, Go module installation, and PostgreSQL database permissions, see the [Installation Guide](./installation.md).

## Initializing the Scheduler

Initialize a new scheduler instance by passing your existing `*sql.DB` connection pool along with functional options:

```go
package main

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	_ "github.com/lib/pq"
)

func createScheduler(db *sql.DB) (*dcron.Scheduler, error) {
	return dcron.New(db,
		// Unique namespace to isolate advisory locks per application
		dcron.WithNamespace("payment-service"),

		// Frequency at which standby nodes check for leadership promotion
		dcron.WithPollInterval(3*time.Second),

		// Dedicated connection pinning for session advisory lock stability
		dcron.WithSessionStableConnection(),

		// Standard structured logger
		dcron.WithLogger(slog.Default()),

		// Grace period allowed on Stop before in-flight jobs are cancelled
		dcron.WithDrainTimeout(30*time.Second),
	)
}
```

## Configuration Options Reference

| Option | Description | Default |
| :--- | :--- | :--- |
| `WithNamespace(name)` | Sets the advisory lock key namespace. Prevents lock collisions between independent applications sharing a database. | `"default"` |
| `WithPollInterval(duration)` | Polling frequency used by standby replicas to attempt lock acquisition (with ±20% jitter). | `5s` |
| `WithDrainTimeout(duration)` | Grace period allowed during `Stop()` for active in-flight jobs to complete. | `30s` |
| `WithStartupTimeout(duration)` | Maximum timeout bounding startup DB operations (lock connection, migrations, keepalives). | `15s` |
| `WithPollTimeout(duration)` | Per-attempt timeout for advisory lock probe queries (`TryLock`, `HoldsLock`). | `5s` |
| `WithDBTimeout(duration)` | Per-operation timeout for history store and admin mutation database queries. | `5s` |
| `WithOpTimeout(duration)` | Alias for `WithDBTimeout`. | `5s` |
| `WithSessionStableConnection()` | Asserts the database connection handle is session-stable (direct Postgres or session-mode pooler). | Required unless dedicated lock configured |
| `WithDedicatedLockDSN(dsn)` | Opens an independent direct connection for advisory locks, bypassing any shared pooler. | None |
| `WithDedicatedLockDriver(drv, dsn)` | Opens an independent direct lock connection using a custom registered driver (e.g., `"pgx"`). | None |
| `WithLeaderForwardURL(url)` | Configures the HTTP URL of the active leader for transparent standby mutation proxying. | None |
| `WithJobHandler(name, fn)` | Registers a named handler function used when restoring dynamic jobs across failover. | None |
| `WithLogger(logger)` | Integrates standard Go `log/slog` structured logging. | `slog.Default()` |
| `WithMetrics(rec)` | Supplies an observability recorder to receive cluster and job metrics. | `metrics.Noop` |
| `WithTracer(tracer)` | Supplies an OpenTelemetry tracer for tracing job executions. | `otel.NoopTracer` |
| `WithHistory(retention)` | Enables the persistent audit store to record execution logs and leader epochs in PostgreSQL. | Disabled |
| `WithSchema(schema)` | Configures the schema name used for history and epoch tables. | `"dcron"` |

## Registering Jobs & Schedules

Register jobs using `scheduler.Add`, providing a unique job name, schedule expression, and an execution handler function:

```go
// 1. Standard 5-field cron syntax (runs daily at 02:00 AM)
err := scheduler.Add("nightly-backup", "0 2 * * *", func(ctx context.Context) error {
	return performBackup(ctx)
})

// 2. Macro syntax (runs once an hour at the top of the hour)
err = scheduler.Add("hourly-sync", "@hourly", func(ctx context.Context) error {
	return syncData(ctx)
})

// 3. Periodic interval syntax (runs every 15 minutes)
err = scheduler.Add("cache-warmer", "@every 15m", func(ctx context.Context) error {
	return refreshCache(ctx)
})

// 4. Timezone override (runs at 09:00 AM New York time)
err = scheduler.Add("ny-morning-report", "CRON_TZ=America/New_York 0 9 * * 1-5", func(ctx context.Context) error {
	return sendMorningReport(ctx)
})
```

## Working with Job Context

Every job receives a standard `context.Context` populated with execution metadata and split-brain fencing tokens:

```go
err := scheduler.Add("process-invoices", "@every 30m", func(ctx context.Context) error {
	// Monotonic leader epoch: strictly increases across leadership changes
	epoch := dcron.Epoch(ctx)

	// Unique idempotency key: deterministic per job execution attempt
	idempotencyKey := dcron.IdempotencyKey(ctx)

	slog.Info("Executing invoice batch",
		"epoch", epoch,
		"idempotency_key", idempotencyKey,
	)

	// Pass epoch into database queries to fence stale writes
	return updateInvoicesWithFencing(ctx, epoch)
})
```

## Scheduler Lifecycle & Graceful Shutdown

Always manage the scheduler lifecycle alongside your application's HTTP/gRPC server:

```go
// Start the scheduler background loop
if err := scheduler.Start(context.Background()); err != nil {
	log.Fatalf("Failed to start scheduler: %v", err)
}

// Trap OS termination signals (SIGINT, SIGTERM)
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
<-sigChan

// Stop gracefully drains in-flight jobs and releases the PostgreSQL advisory lock
drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := scheduler.Stop(drainCtx); err != nil {
	log.Printf("Scheduler shutdown warning: %v", err)
}
```

## Runtime Job Controls & Durability

d-cron allows dynamically inspecting, pausing, resuming, adding, and removing jobs while the scheduler is running:

```go
// Pause a job (persisted in database, survives leader failover)
err := scheduler.Pause("nightly-backup")

// Resume a paused job
err = scheduler.Resume("nightly-backup")

// Remove a registered job (durably removed across the cluster)
err = scheduler.Remove("cache-warmer")

// Add a job dynamically at runtime (persisted in database, deduplicated against retries)
err = scheduler.AddDynamic("report-export", "@every 30m", func(ctx context.Context) error {
	return generateExport(ctx)
})

// Check current leadership status of this replica
isLeader := (scheduler.Leadership() == dcron.LeadershipLeader)

// Verify database pool and dedicated lock connection health
err = scheduler.HealthCheck(ctx)
```

### Cluster-Wide Admin Mutations & Failover
- **Durable Persistence:** All mutations (`Pause`, `Resume`, `Remove`, `AddDynamic`) are saved to PostgreSQL (`job_state` table) before in-memory state is altered. If the database write fails, an error is returned and no in-memory state is changed.
- **Failover Reconciliation:** When a standby replica acquires the advisory lock and is promoted to leader, it queries `job_state` and reconciles all job definitions: paused jobs remain paused without firing, removed jobs are unmarshaled from memory, and dynamic jobs are restored into the scheduling queue.
- **Dynamic Job Handlers:** To ensure dynamic jobs can execute when leadership fails over to another node, register named handler functions using `dcron.WithJobHandler("report-export", fn)` or `scheduler.RegisterHandler("report-export", fn)`.
- **Standby Mutation Proxying:** If an admin mutation (`POST /dcron/admin/jobs/{name}/pause`, `resume`, `DELETE`, etc.) hits a standby node, the standby automatically proxies the request to the active leader if `WithLeaderForwardURL("http://dcron-leader:8080")` is configured; otherwise, it returns `409 Conflict` (`dcron: not leader`).


## Running Included Examples

The `examples/` directory contains complete, runnable sample applications:

### Minimal Standalone Runner
```bash
go run ./examples/minimal/main.go -db "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable"
```

### Dashboard & Metrics Web Runner
```bash
go run ./examples/with-dashboard/main.go -port 8080
```
Open `http://localhost:8080/dcron/ui/` in your browser to inspect jobs and history.

## Next Steps

- Learn about [Core Architecture](./architecture/advisory-locks.md) and advisory lock internals.
- Explore [Split-Brain & Fencing](./architecture/fencing-tokens.md) to safeguard critical database updates.
- Check [Execution Safety & Retries](./guides/execution-safety.md) for panic recovery and backoff retries.
