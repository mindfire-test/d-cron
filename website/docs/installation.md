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
| **Go** | 1.21+ | 1.22+ | Uses standard `context`, `sync`, and `log/slog` |
| **PostgreSQL** | 12.0+ | 14.0+ | Supports 32-bit and 64-bit Advisory Locks |
| **Go Database Driver** | `database/sql` (`lib/pq` or `pgx/v5`) | `pgx/v5` (stdlib mode) | Any standard `*sql.DB` connection pool |

---

## 📥 Module Installation

Install `d-cron` using standard Go modules:

```bash
go get github.com/mindfiredigital/d-cron@latest
```

If you are using optional observability integrations or database adapters, you can also fetch the optional packages:

```bash
# Optional: Prometheus & OpenTelemetry instrumentation
go get github.com/mindfiredigital/d-cron/metrics
go get github.com/mindfiredigital/d-cron/otel

# Optional: Embedded Web Dashboard UI
go get github.com/mindfiredigital/d-cron/ui
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

If you enable execution history logging (`dcron.WithHistoryStore`), `d-cron` records job run status, duration, leader instance ID, and error messages to a database table.

Execute the following DDL migration script to create the audit table:

```sql
CREATE TABLE IF NOT EXISTS dcron_job_history (
    id BIGSERIAL PRIMARY KEY,
    job_name VARCHAR(255) NOT NULL,
    schedule VARCHAR(100) NOT NULL,
    instance_id VARCHAR(255) NOT NULL,
    leader_epoch BIGINT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    duration_ms BIGINT NOT NULL,
    status VARCHAR(50) NOT NULL, -- 'SUCCESS', 'FAILED', 'CANCELLED', 'SKIPPED'
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dcron_history_job_started 
ON dcron_job_history (job_name, started_at DESC);
```

---

## 🔧 Basic Initialization Pattern

Here is the standard initialization pattern inside a Go application main file:

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/mindfiredigital/d-cron/dcron"
)

func main() {
	// 1. Open PostgreSQL connection
	db, err := sql.Open("pgx", "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable")
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer db.Close()

	// 2. Initialize d-cron Scheduler
	scheduler, err := dcron.New(
		db,
		dcron.WithNamespace("myapp-scheduler"),
		dcron.WithInstanceID("pod-replica-01"),
	)
	if err != nil {
		log.Fatalf("failed to create scheduler: %v", err)
	}

	// 3. Register Jobs
	err = scheduler.Add("cleanup-temp-files", "0 */6 * * *", func(ctx context.Context) error {
		fmt.Println("Cleaning up temporary files...")
		return nil
	})
	if err != nil {
		log.Fatalf("failed to register job: %v", err)
	}

	// 4. Start Scheduler in Background
	if err := scheduler.Start(context.Background()); err != nil {
		log.Fatalf("scheduler start error: %v", err)
	}

	// Wait for signal and shutdown gracefully
	// scheduler.Stop()
}
```

---

## ⏭️ Next Steps

- Explore [PostgreSQL Advisory Locks](./architecture/advisory-locks.md) to learn how `d-cron` acquires locks without polling spikes.
- Read [Leader Election](./architecture/leader-election.md) to understand continuous heartbeat and failover mechanics.
