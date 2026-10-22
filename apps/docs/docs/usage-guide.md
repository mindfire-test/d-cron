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

func createScheduler(db *sql.DB) (*dcron.Dcron, error) {
	return dcron.New(db,
		// Unique namespace to isolate advisory locks per application
		dcron.WithNamespace("payment-service"),

		// Frequency at which standby nodes check for leadership promotion
		dcron.WithPollInterval(3*time.Second),

		// Dedicated connection pinning for session advisory lock stability
		dcron.WithSessionStableConnection(),

		// Standard structured logger
		dcron.WithLogger(slog.Default()),

		// Default execution timeout applied to registered jobs
		dcron.WithTimeout(10*time.Minute),
	)
}
```

## Configuration Options Reference

| Option | Description | Default |
| :--- | :--- | :--- |
| `WithNamespace(name)` | Sets the advisory lock key namespace. Prevents lock collisions between independent applications sharing a database. | `"default"` |
| `WithPollInterval(duration)` | Polling frequency used by standby replicas to attempt lock acquisition. | `5s` |
| `WithSessionStableConnection()` | Pins the election loop to a dedicated, persistent `*sql.Conn` to prevent connection-pool recycling issues. | Enabled |
| `WithLogger(logger)` | Integrates standard Go `log/slog` structured logging. | `slog.Default()` |
| `WithTimeout(duration)` | Sets a global execution timeout for all jobs unless overridden per job. | None |
| `WithOverlapPolicy(policy)` | Configures behavior when a job trigger arrives while the previous run is still active (`OverlapSkip` or `OverlapAllow`). | `OverlapSkip` |
| `WithHistory(retention)` | Enables the persistent audit store to record execution logs and leader epochs in PostgreSQL. | Disabled |

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

## Runtime Job Controls

d-cron allows dynamically inspecting, triggering, or removing jobs while the scheduler is running:

```go
// Manually trigger a job immediately outside its normal schedule
err := scheduler.Trigger("nightly-backup")

// Remove a registered job at runtime
err = scheduler.Remove("cache-warmer")

// Check current leadership status of this container instance
isLeader := scheduler.IsLeader()
```

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
