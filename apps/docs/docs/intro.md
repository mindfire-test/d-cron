---
id: intro
title: Introduction
sidebar_label: Introduction
slug: /intro
---

# Introduction

d-cron is a lightweight, distributed cron scheduler library for Go that coordinates scheduled background jobs across multiple application replicas using only your existing PostgreSQL database.

## What Does d-cron Do?

When you scale a Go application to multiple container replicas (in Kubernetes, Docker, or across cloud virtual machines), traditional in-process cron schedulers trigger jobs on every single replica simultaneously. For example, a nightly billing job scheduled for 2:00 AM runs $N$ times if you run $N$ replicas.

d-cron solves this through distributed single-leader coordination:
- **Leader Election via PostgreSQL**: Automatically elects one replica as the active Leader to run the scheduler clock.
- **Single Active Leader Coordination**: Scheduled jobs are dispatched by the single elected leader replica, ensuring at-most-once delivery by default, with deterministic idempotency keys provided to user jobs for at-least-once safety during failovers.
- **Automatic Failover**: If the active leader container shuts down, restarts, or loses connectivity, a standby replica cleanly takes over leadership.
- **Zero Extra Infrastructure**: Operates using standard PostgreSQL session-bound advisory locks. No Redis clusters, etcd, ZooKeeper, or external scheduler daemons are required.

## Who Is It For?

d-cron is designed for:
- **Go Developers & Teams**: Building web applications, APIs, and microservices in Go that require scheduled periodic tasks.
- **Horizontally Scaled Workloads**: Applications deployed with multiple replicas in Kubernetes or Docker where duplicate cron execution causes data inconsistency, duplicate notifications, or wasted resources.
- **PostgreSQL Users**: Any application that already connects to PostgreSQL and wants simple, reliable distributed cron without introducing new infrastructure dependencies like Redis, RabbitMQ, or Temporal.
- **Simplicity Seekers**: Teams that want to execute direct in-process Go functions (`scheduler.Add`) without setting up complex worker queues, serialization formats, or external daemon clusters like Dkron.

## Quick Start

A complete, runnable Go application connecting to PostgreSQL, initializing d-cron, and registering a distributed cron job:

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
	// 1. Connect to PostgreSQL
	dsn := "postgres://postgres:postgres@localhost:5432/myapp?sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// 2. Initialize d-cron Scheduler
	scheduler, err := dcron.New(db,
		dcron.WithNamespace("billing-service"),
		dcron.WithPollInterval(3*time.Second),
		dcron.WithSessionStableConnection(),
		dcron.WithLogger(slog.Default()),
	)
	if err != nil {
		log.Fatalf("Failed to initialize scheduler: %v", err)
	}

	// 3. Register a distributed cron job (e.g. daily at 2:00 AM)
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

	// 4. Start the scheduler in background
	if err := scheduler.Start(context.Background()); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}

	slog.Info("Scheduler running. Press Ctrl+C to stop.")

	// 5. Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := scheduler.Stop(drainCtx); err != nil {
		log.Printf("Scheduler shutdown warning: %v", err)
	}
}
```

For prerequisites and database setup, see [Installation](./installation.md). For advanced options, cron syntax, and runtime controls, see the [Usage Guide](./usage-guide.md).

## Key Features

- **Single Leader Coordination**: Exactly 1 active leader pod runs the min-heap timer clock. Zero thundering-herd lock racing or database CPU spikes at minute boundaries.
- **Zero Extra Infrastructure**: Operates purely on PostgreSQL session-bound advisory locks. No Redis, ZooKeeper, etcd, or sidecars.
- **Split-Brain Fencing**: Injects monotonic leader epoch tokens (`LeaderEpoch`) into job `context.Context` to fence stale database writes if network partitioning occurs.
- **Zero Migration Default**: Requires zero database tables and zero DDL migrations. Creates tables only if you opt in to durable execution history.
- **In-Process Go Functions**: Register standard Go functions directly with `scheduler.Add`. No JSON serialization or worker queue decoupling required.
- **Execution Safety & Panic Recovery**: Built-in panic recovery, configurable execution timeouts, exponential backoff retries, and overlap prevention policies.
- **Built-in Observability**: Includes an embedded Web Dashboard UI, Prometheus metrics exporter, OpenTelemetry tracing, and an HTTP REST Admin API.

## Why d-cron?

When building modern Go microservices, you face three problematic alternatives for background scheduling:

1. **In-process schedulers (like robfig/cron)**:
   - Great for single instances, but when scaled to multiple replicas, every instance runs every job simultaneously.
2. **Per-job lock racing (like gocron with Redis)**:
   - When a minute boundary arrives, all instances simultaneously race to acquire locks in Redis or PostgreSQL, creating connection spikes and thundering-herd stampedes.
3. **Heavy task queues and workflow engines (River, Asynq, Temporal)**:
   - Introduce massive architectural overhead: custom queue tables, worker polling loops, Redis clusters, or external daemons for tasks that only need periodic cron execution.

d-cron takes a simpler, more robust path: a **single leader election** via lightweight PostgreSQL advisory locks. The elected leader runs a precise in-memory timer, dispatches jobs asynchronously, and automatically yields leadership if it shuts down or partitions.

## Comparison with Similar Tools

| Factor | robfig/cron | gocron | asynq | river | d-cron |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Architecture** | In-Process | In-Process | Task Queue | Task Queue | **In-Process Library** |
| **Infrastructure** | None | Redis | Redis | Postgres + Tables | **Postgres (Zero extra tables)** |
| **Execution Model** | Direct Go | Direct Go | Async Worker Queue | Async Worker Queue | **Direct In-Process Go** |
| **Coordination** | None | Per-Job Lock Race | Queue Enqueue | Leader Election | **Single Leader Advisory Lock** |
| **Thundering Herd** | N/A | High (at boundary) | None | Low | **Zero (1 Leader Clock)** |
| **Split-Brain Fencing** | No | No | No | No | **Monotonic Epoch Tokens** |
| **License** | Open Source | Open Source | Open Source | Paid / Pro Features | **100% Open Source (MIT)** |

## Next Steps

- Follow the [Installation Guide](./installation.md) for requirements and database configuration.
- Read the [Usage Guide](./usage-guide.md) for scheduler options, cron syntax, and job lifecycle patterns.
- Learn more about the [Core Architecture](./architecture/advisory-locks.md) and [Split-Brain Fencing](./architecture/fencing-tokens.md).
