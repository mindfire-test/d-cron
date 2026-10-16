---
id: quickstart
title: Quickstart Guide
sidebar_label: Quickstart
---

# Quickstart Guide

This guide demonstrates how to install `d-cron` and integrate it into a Go application connected to PostgreSQL.

---

## 📦 Installation

Add `d-cron` to your Go project using `go get`:

```bash
go get github.com/mindfire-test/d-cron
```

---

## 🛠️ Basic Usage Example

```go
package main

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
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
	// WithNamespace sets the lock scope; WithSessionStableConnection asserts a session-mode connection.
	scheduler, err := dcron.New(db,
		dcron.WithNamespace("billing-service"),
		dcron.WithPollInterval(3*time.Second),
		dcron.WithSessionStableConnection(),
		dcron.WithLogger(slog.Default()),
	)
	if err != nil {
		log.Fatalf("Failed to construct scheduler: %v", err)
	}

	// 3. Register a job using standard 5-field cron syntax ("minute hour dom month dow")
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
	ctx := context.Background()
	if err := scheduler.Start(ctx); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}

	slog.Info("Scheduler running. Press Ctrl+C to exit.")

	// Wait for shutdown signal (e.g., SIGTERM)
	time.Sleep(1 * time.Minute)

	// 5. Stop the scheduler gracefully with a drain timeout
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := scheduler.Stop(stopCtx); err != nil {
		log.Printf("Scheduler shutdown warning: %v", err)
	}
}
```

---

## 🔍 How it Works in Production

When `N` replicas of your application run this same code:

1. **Election Phase**: All replicas attempt to acquire the namespace lock key via `pg_try_advisory_lock`.
2. **Leader Selected**: Exactly **1 replica becomes Leader** and starts running the min-heap cron clock.
3. **Standby Polling**: The remaining `N-1` replicas become **Standbys**, checking for promotion at a randomized polling interval (e.g. 5s ± 20% jitter).
4. **Failover**: If the Leader pod exits or crashes, PostgreSQL automatically releases the advisory lock. One of the Standby replicas acquires the lock and assumes leadership within 1 poll interval.
