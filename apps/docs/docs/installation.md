---
id: installation
title: Installation
sidebar_label: Installation
slug: /installation
---

# Installation

This page covers system requirements, module installation, and database permissions for d-cron.

## Requirements

| Requirement | Minimum Version | Recommended | Notes |
| :--- | :--- | :--- | :--- |
| **Go** | 1.21+ | 1.23+ | Uses standard library `context`, `sync`, and `log/slog` |
| **PostgreSQL** | 12.0+ | 14.0+ | Supports 64-bit Session Advisory Locks (`pg_try_advisory_lock`) |
| **Go Database Driver** | `database/sql` (`lib/pq` or `pgx/v5`) | `pgx/v5` (stdlib mode) or `lib/pq` | Any standard `*sql.DB` connection pool |

## Module Installation

Install d-cron into your Go project using standard Go modules:

```bash
go get github.com/mindfire-test/d-cron@latest
```

All sub-packages (metrics exporter, OpenTelemetry tracing, and embedded web dashboard UI) are included in the same repository:

```go
import (
    "github.com/mindfire-test/d-cron/dcron"
    "github.com/mindfire-test/d-cron/metrics"
    "github.com/mindfire-test/d-cron/otel"
    "github.com/mindfire-test/d-cron/ui"
)
```

## Database Setup & Permissions

### Zero-Migration Default Mode

By default, d-cron operates using **PostgreSQL Session Advisory Locks**:
- Requires **zero schema migrations**.
- Creates **zero database tables**.
- Requires no special DDL (`CREATE TABLE`) privileges.

All d-cron requires is a standard PostgreSQL role with basic `CONNECT` permissions on your existing application database:

```sql
GRANT CONNECT ON DATABASE myapp_db TO app_user;
```

### Optional History & Audit Store Setup

If you choose to enable durable execution history logging (`dcron.WithHistory(retention)`), d-cron automatically creates and migrates its required schema (default `"dcron"`) and tables (`leader_epoch`, `execution`, and `job_state`) under a serialized advisory lock.

If your database user does not have DDL privileges, you can execute the idempotent DDL migration script manually ahead of time:

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

CREATE TABLE IF NOT EXISTS dcron.job_state (
    namespace   TEXT NOT NULL,
    job_name    TEXT NOT NULL,
    paused      BOOLEAN NOT NULL DEFAULT false,
    removed     BOOLEAN NOT NULL DEFAULT false,
    spec        TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, job_name)
);

CREATE INDEX IF NOT EXISTS execution_job_time_idx 
    ON dcron.execution (namespace, job_name, scheduled_at DESC);
CREATE INDEX IF NOT EXISTS execution_prune_idx 
    ON dcron.execution (namespace, finished_at);
CREATE INDEX IF NOT EXISTS execution_prune_running_idx 
    ON dcron.execution (namespace, started_at) WHERE status = 'running';
```

## Next Steps

- Follow the [Usage Guide](./usage-guide.md) to configure options, register jobs, and manage the scheduler lifecycle.
- Explore the [Core Architecture](./architecture/advisory-locks.md) to understand PostgreSQL advisory locks and leader election.
