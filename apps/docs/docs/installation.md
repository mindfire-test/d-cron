---
id: installation
title: Installation & Setup
sidebar_label: Installation
---

# 📦 Installation & Prerequisites

`d-cron` is an embeddable Go library with zero external daemon dependencies. Installing and configuring it into an existing or new Go application requires minimal steps.

---

## 📋 Requirements

| Requirement | Minimum Version | Recommended | Notes |
| :--- | :--- | :--- | :--- |
| **Go** | 1.21+ | 1.23+ | Uses standard `context`, `sync`, and `log/slog` |
| **PostgreSQL** | 12.0+ | 14.0+ | Supports 64-bit Advisory Locks |
| **Go Database Driver** | `database/sql` (`lib/pq` or `pgx/v5`) | `pgx/v5` (stdlib mode) or `lib/pq` | Any standard `*sql.DB` connection pool |

---

## 📥 Module Installation

Install `d-cron` using standard Go modules:

```bash
go get github.com/mindfire-test/d-cron@latest
```

Optional sub-packages (metrics, tracing, web dashboard UI) are in the same module and require no extra downloads:

```go
import (
    "github.com/mindfire-test/d-cron/dcron"
    "github.com/mindfire-test/d-cron/metrics"
    "github.com/mindfire-test/d-cron/otel"
    "github.com/mindfire-test/d-cron/ui"
)
```

---

## 🗄️ Database Setup & Permissions

### Zero-Migration Default Mode

By default, `d-cron` operates using **PostgreSQL Session Advisory Locks**. 
- It requires **zero schema migrations**.
- It creates **zero database tables**.
- It requires no special DDL (`CREATE TABLE`) privileges.

All `d-cron` needs is a standard Postgres role with baseline `CONNECT` permissions on your existing application database:

```sql
GRANT CONNECT ON DATABASE myapp_db TO app_user;
```

### Optional History & Audit Store Setup

If you enable durable execution history logging (`dcron.WithHistory(retention)`), `d-cron` automatically creates and migrates its required schema (default `"dcron"`) and tables (`leader_epoch` and `execution`) under a serialized advisory lock.

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
    status        TEXT NOT NULL,
    attempt       INT NOT NULL DEFAULT 1,
    duration_ms   BIGINT,
    error         TEXT,
    instance_id   TEXT NOT NULL,
    leader_epoch  BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS execution_job_time_idx 
    ON dcron.execution (namespace, job_name, scheduled_at DESC);
```

---

## 🔧 Basic Initialization Pattern

Here is the standard initialization pattern inside a Go application `main.go`:

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
	"github.com/mindfire-test/d-cron/dcron"
)

func main() {
	// 1. Open PostgreSQL connection
	db, err := sql.Open("postgres", "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable")
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer db.Close()

	// 2. Initialize d-cron Scheduler
	scheduler, err := dcron.New(
		db,
		dcron.WithNamespace("myapp-scheduler"),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(3*time.Second),
	)
	if err != nil {
		log.Fatalf("failed to create scheduler: %v", err)
	}

	// 3. Register Jobs
	err = scheduler.Add("cleanup-temp-files", "0 */6 * * *", func(ctx context.Context) error {
		fmt.Printf("Cleaning up temporary files (leader epoch: %d)...\n", dcron.Epoch(ctx))
		return nil
	})
	if err != nil {
		log.Fatalf("failed to register job: %v", err)
	}

	// 4. Start Scheduler in Background
	if err := scheduler.Start(context.Background()); err != nil {
		log.Fatalf("scheduler start error: %v", err)
	}

	// 5. Run application, then shutdown gracefully
	stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := scheduler.Stop(stopCtx); err != nil {
		log.Printf("scheduler stop warning: %v", err)
	}
}
```

---

## ⏭️ Next Steps

- Explore [PostgreSQL Advisory Locks](./architecture/advisory-locks.md) to learn how `d-cron` acquires locks without polling spikes.
- Read [Leader Election](./architecture/leader-election.md) to understand continuous heartbeat and failover mechanics.
