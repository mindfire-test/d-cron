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

### 1. Check Leader Status & Jobs Snapshot

```bash
curl -s http://localhost:8080/api/v1/dcron/status | jq .
```

**Output:**
```json
{
  "instance": "a1b2c3d4e5f60718",
  "namespace": "orders-service",
  "is_leader": true,
  "jobs": [
    {
      "name": "daily-report",
      "spec": "0 2 * * *",
      "next_run": "2026-10-22T02:00:00Z",
      "last_run": "2026-10-21T02:00:00Z",
      "last_outcome": "success",
      "running": false,
      "paused": false
    }
  ]
}
```

---

### 2. Pause and Resume Jobs

```bash
# Pause future job triggers
curl -i -X POST http://localhost:8080/api/v1/dcron/jobs/daily-report/pause

# Resume job triggers
curl -i -X POST http://localhost:8080/api/v1/dcron/jobs/daily-report/resume
```

---

### 3. Remove a Job Dynamically

```bash
curl -i -X DELETE http://localhost:8080/api/v1/dcron/jobs/daily-report
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
