---
id: history-store
title: Audit & History Store
sidebar_label: Execution History Store
---

# Audit & History Store

By default, `d-cron` operates with zero database tables. When compliance, auditing, missed-run catch-up, or dashboard monitoring requires durable tracking, `d-cron` provides an **opt-in durable execution history store** backed by PostgreSQL.

## Enabling History Logging

Enable history persistence by passing `dcron.WithHistory(retention)` to `dcron.New`:

```go
package main

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/lib/pq"
	"github.com/mindfire-test/d-cron/dcron"
)

func main() {
	db, err := sql.Open("postgres", "postgres://user:pass@localhost:5432/myapp?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	scheduler, err := dcron.New(
		db,
		dcron.WithNamespace("orders-service"),
		dcron.WithSessionStableConnection(),
		// Retain execution records for 7 days
		dcron.WithHistory(7*24*time.Hour),
		// Optional: custom schema (defaults to "dcron")
		dcron.WithSchema("dcron"),
	)
	if err != nil {
		log.Fatalf("failed to init scheduler: %v", err)
	}
	// ...
}
```

## Automatic Schema Migrations

When `dcron.WithHistory` is enabled:
1. `d-cron` acquires a dedicated advisory lock (`d-cron:migration:<schema>`) to serialize schema creation across multiple starting replicas.
2. It executes idempotent DDL creating the configured schema (default `dcron`) and two core tables:

### 1. `leader_epoch` Table
Stores the active leader instance and current monotonic election epoch for split-brain fencing:

```sql
CREATE SCHEMA IF NOT EXISTS dcron;

CREATE TABLE IF NOT EXISTS dcron.leader_epoch (
    namespace   TEXT PRIMARY KEY,
    epoch       BIGINT NOT NULL,
    instance_id TEXT NOT NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 2. `execution` Table
Records individual job execution attempts, timestamps, latencies, and outcomes:

```sql
CREATE TABLE IF NOT EXISTS dcron.execution (
    id            BIGSERIAL PRIMARY KEY,
    namespace     TEXT NOT NULL,
    job_name      TEXT NOT NULL,
    scheduled_at  TIMESTAMPTZ NOT NULL,
    started_at    TIMESTAMPTZ NOT NULL,
    finished_at   TIMESTAMPTZ,
    status        TEXT NOT NULL, -- "running", "success", "failed", "panicked", "timeout"
    attempt       INT NOT NULL DEFAULT 1,
    duration_ms   BIGINT,
    error         TEXT,
    instance_id   TEXT NOT NULL,
    leader_epoch  BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS execution_job_time_idx
    ON dcron.execution (namespace, job_name, scheduled_at DESC);
```

## Automatic Retention & Pruning

When `WithHistory(retention)` is configured with a positive retention duration (e.g. `7 * 24 * time.Hour`), the active cluster leader periodically prunes executions older than the retention boundary:

```sql
DELETE FROM dcron.execution
WHERE namespace = $1 AND finished_at < $2
```

This ensures the audit table remains compact without requiring manual cron cleanup jobs or vacuum bloat.

## Querying Execution History

You can query execution history directly in SQL or inspect recent runs programmatically:

```go
// SQL query for recent job failures:
rows, err := db.QueryContext(ctx, `
    SELECT job_name, scheduled_at, duration_ms, status, error
    FROM dcron.execution
    WHERE namespace = 'orders-service' AND status != 'success'
    ORDER BY scheduled_at DESC
    LIMIT 50
`)
```

When used alongside the embedded dashboard (`ui.Handler`), historical rows are rendered dynamically in the dashboard view.
