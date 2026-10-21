---
id: quickstart
title: Usage Guide
sidebar_label: Usage Guide
---

# Usage Guide & Quickstart

This guide demonstrates how to install `d-cron`, integrate it into a Go application with PostgreSQL, and configure cron schedules.

---

## Installation

Add `d-cron` to your Go project using `go get`:

```bash
go get github.com/mindfire-test/d-cron
```

---

## Complete Working Example

```go
package main

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	_ "github.com/lib/pq" // PostgreSQL driver
)

func main() {
	// 1. Open PostgreSQL connection
	dsn := "postgres://postgres:postgres@localhost:5432/myapp?sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	// 2. Construct d-cron Scheduler
	scheduler, err := dcron.New(db,
		dcron.WithNamespace("billing-service"),
		dcron.WithPollInterval(3*time.Second),
		dcron.WithSessionStableConnection(),
		dcron.WithLogger(slog.Default()),
	)
	if err != nil {
		log.Fatalf("Failed to construct scheduler: %v", err)
	}

	// 3. Register a job using standard 5-field cron syntax
	err = scheduler.Add("daily-cleanup", "0 2 * * *", func(ctx context.Context) error {
		slog.Info("Running daily cleanup job",
			"job", "daily-cleanup",
			"epoch", dcron.Epoch(ctx),
			"idempotency_key", dcron.IdempotencyKey(ctx),
		)
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to register job: %v", err)
	}

	// 4. Start the scheduler background loop
	if err := scheduler.Start(context.Background()); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}

	slog.Info("Scheduler running. Press Ctrl+C to exit.")

	// 5. Graceful shutdown on SIGTERM / SIGINT
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	if err := scheduler.Stop(); err != nil {
		log.Printf("Scheduler shutdown warning: %v", err)
	}
}
```

---

## Cron Expression Syntax

`d-cron` supports standard 5-field cron syntax, optional 6-field second-level precision, macros (`@every 5m`), and timezone overrides (`CRON_TZ`).

```
 ┌───────────── second (optional with WithSecondsField(), 0 - 59)
 │ ┌───────────── minute (0 - 59)
 │ │ ┌───────────── hour (0 - 23)
 │ │ │ ┌───────────── day of month (1 - 31)
 │ │ │ │ ┌───────────── month (1 - 12 or JAN-DEC)
 │ │ │ │ │ ┌───────────── day of week (0 - 6 or SUN-SAT)
 │ │ │ │ │ │
 * * * * * *
```

### Common Cron Expressions

| Expression | Schedule | Description |
| :--- | :--- | :--- |
| `* * * * *` | Every minute | High-frequency polling |
| `0 * * * *` | Every hour at :00 | Hourly analytics rollups |
| `0 0 * * *` | Midnight every day | Daily settlement / batch purge |
| `0 2 * * *` | 2:00 AM daily | Nightly backups / vacuum |
| `0 9 * * 1-5` | 9:00 AM Mon-Fri | Weekday business notifications |
| `0 0 1 * *` | 1st of every month | Monthly subscription renewals |
| `*/15 * * * *` | Every 15 minutes | Periodic cache warm-up |

### Macro Specifiers

| Macro | Equivalent Cron | Description |
| :--- | :--- | :--- |
| `@hourly` | `0 * * * *` | Run once an hour at the top of the hour |
| `@daily` (or `@midnight`) | `0 0 * * *` | Run once a day at midnight |
| `@weekly` | `0 0 * * 0` | Run once a week at midnight on Sunday |
| `@monthly` | `0 0 1 * *` | Run once a month at midnight on the 1st |
| `@every <duration>` | Custom Duration | Run periodically (e.g. `@every 5m`, `@every 1h30m`) |

### Timezone Overrides (`CRON_TZ`)

You can specify explicit timezones per job using `CRON_TZ`:

```go
// Runs every day at 09:00 AM America/New_York regardless of server local time
scheduler.Add("ny-morning-report", "CRON_TZ=America/New_York 0 9 * * *", jobHandler)
```

---

## How it Works Across Scaled Replicas

When `N` replicas of your application run this same code:

1. **Election Phase**: All replicas attempt to acquire the namespace lock key via `pg_try_advisory_lock`.
2. **Leader Selected**: Exactly **1 replica becomes Leader** and starts running the min-heap cron clock.
3. **Standby Polling**: The remaining `N-1` replicas become **Standbys**, checking for promotion at a randomized polling interval (e.g. 5s ± 20% jitter).
4. **Automatic Failover**: If the Leader pod exits or crashes, PostgreSQL automatically releases the advisory lock. One of the Standby replicas acquires the lock and assumes leadership seamlessly.
