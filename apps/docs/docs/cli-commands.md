---
id: cli-commands
title: CLI & Administration Tooling
sidebar_label: CLI & Admin Tools
---

# 💻 CLI & Administration Tooling

While `d-cron` is primarily used as an embedded Go library, `d-cron` provides command-line runner utilities and REST administration tools to manage job clusters from shell scripts, CI/CD pipelines, or terminal sessions.

---

## 🛠️ Built-in Admin HTTP CLI Client

You can interact with a running `d-cron` cluster via its REST API (see [Admin API Guide](./guides/admin-api.md)) using standard `curl` or HTTP CLI clients.

### 1. Check Leader Status & Topology

```bash
curl -s http://localhost:8080/api/v1/dcron/status | jq .
```

**Output:**
```json
{
  "instance_id": "pod-orders-7f9b8-x2k9l",
  "is_leader": true,
  "leader_epoch": 14,
  "namespace": "orders-service",
  "uptime_seconds": 84201,
  "active_jobs_count": 8
}
```

---

### 2. List All Registered Jobs

```bash
curl -s http://localhost:8080/api/v1/dcron/jobs | jq .
```

**Output:**
```json
[
  {
    "name": "cleanup-temp-files",
    "schedule": "0 */6 * * *",
    "next_run": "2026-09-21T12:00:00Z",
    "last_status": "SUCCESS",
    "last_duration_ms": 142
  },
  {
    "name": "generate-invoices",
    "schedule": "0 1 * * *",
    "next_run": "2026-09-22T01:00:00Z",
    "last_status": "SUCCESS",
    "last_duration_ms": 4820
  }
]
```

---

### 3. Manually Trigger a Job via Shell

Trigger immediate out-of-schedule execution of a registered job:

```bash
curl -X POST http://localhost:8080/api/v1/dcron/jobs/generate-invoices/trigger
```

---

### 4. Pause and Resume Jobs

```bash
# Pause job triggers
curl -X POST http://localhost:8080/api/v1/dcron/jobs/generate-invoices/pause

# Resume job triggers
curl -X POST http://localhost:8080/api/v1/dcron/jobs/generate-invoices/resume
```

---

## 🏃 Running Examples via `go run`

The `d-cron` repository contains ready-to-run CLI examples inside `examples/`:

### Minimal Standalone Runner
```bash
go run ./examples/minimal/main.go -db "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable"
```

### Dashboard & Metrics Web Runner
```bash
go run ./examples/with-dashboard/main.go -port 8080
```
Then navigate your browser to `http://localhost:8080/dcron/ui/`.
