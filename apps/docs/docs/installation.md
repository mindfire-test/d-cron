---
id: installation
title: Installation & Setup
sidebar_label: Installation
---

# 📦 Installation & Setup

`d-cron` is an embeddable Go library with zero external daemon dependencies. Installing and configuring it into an existing or new Go application requires minimal steps.

---

## 📋 Requirements

| Requirement | Minimum Version | Recommended | Notes |
| :--- | :--- | :--- | :--- |
| **Go** | 1.21+ | 1.23+ | Uses standard `context`, `sync`, and `log/slog` |
| **PostgreSQL** | 12.0+ | 14.0+ | Supports 64-bit Session Advisory Locks |
| **Go Database Driver** | `database/sql` (`lib/pq` or `pgx/v5`) | `pgx/v5` (stdlib mode) or `lib/pq` | Any standard `*sql.DB` connection pool |

---

## 📥 Module Installation

Install `d-cron` using standard Go modules:

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

## 🗄️ Database Setup & Permissions

### Zero-Migration Default Mode

By default, `d-cron` operates using **PostgreSQL Session Advisory Locks**:
- It requires **zero schema migrations**.
- It creates **zero database tables**.
- It requires no special DDL (`CREATE TABLE`) privileges.

All `d-cron` requires is a standard PostgreSQL role with `CONNECT` permissions on your existing application database:

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

## 💻 Running CLI Examples & Tools

The `d-cron` repository contains ready-to-run CLI examples inside `examples/`:

### Minimal Standalone Runner
```bash
go run ./examples/minimal/main.go -db "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable"
```

### Dashboard & Metrics Web Runner
```bash
go run ./examples/with-dashboard/main.go -port 8080
```
Then open `http://localhost:8080/dcron/ui/` in your browser.

---

## ⏭️ Next Steps

- Follow the [Quickstart Guide](./quickstart.md) to register your first distributed job.
- Explore [Core Architecture](./architecture/advisory-locks.md) to understand single-leader election and the in-memory clock engine.
