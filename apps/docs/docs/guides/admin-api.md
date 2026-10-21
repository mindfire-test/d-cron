---
id: admin-api
title: HTTP Admin API & Web UI
sidebar_label: Admin API & Dashboard
---

# 🖥️ HTTP Admin API & Embedded Dashboard

`d-cron` provides both a server-rendered Web UI dashboard and an HTTP administration REST API to monitor cluster leadership, inspect registered jobs, pause/resume executions, and view execution history.

---

## 🌐 Mounting the Embedded Dashboard UI

The dashboard is completely self-contained in `github.com/mindfire-test/d-cron/ui` with zero external CDN dependencies, making it safe for air-gapped environments.

Mount the dashboard handler on any standard HTTP router using `ui.Handler`:

```go
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"

	_ "github.com/lib/pq"
	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/ui"
)

func main() {
	db, _ := sql.Open("postgres", "postgres://user:pass@localhost:5432/myapp?sslmode=disable")
	scheduler, _ := dcron.New(db, dcron.WithNamespace("orders-service"))

	// Build overview provider from scheduler state
	overview := func() ui.Overview {
		var rows []ui.JobRow
		for _, j := range scheduler.Jobs() {
			rows = append(rows, ui.JobRow{
				Name:           j.Name,
				Spec:           j.Spec,
				NextRun:        j.NextRun,
				LastRun:        j.LastRun,
				LastOutcome:    j.LastOutcome,
				LastError:      j.LastError,
				LastDurationMS: j.LastDuration.Milliseconds(),
				Running:        j.Running,
			})
		}
		return ui.Overview{
			Namespace:  scheduler.Namespace(),
			InstanceID: scheduler.InstanceID(),
			LockKey:    scheduler.Key(),
			Leadership: scheduler.Leadership().String(),
			Jobs:       rows,
		}
	}

	// Mount Dashboard UI at /dcron/ui
	http.Handle("/dcron/ui", ui.Handler(overview, nil))

	scheduler.Start(context.Background())
	log.Println("Dashboard available at http://localhost:8080/dcron/ui")
	http.ListenAndServe(":8080", nil)
}
```

---

## 📡 REST Admin API Endpoints

To programmatically inspect cluster topology or manipulate jobs, mount `dcron.AdminHandler`:

```go
// Optional auth guard callback
auth := func(r *http.Request) bool {
    // e.g. verify Bearer token or basic auth
    return r.Header.Get("Authorization") == "Bearer secret-token"
}

// Mount REST admin API on http.ServeMux
http.Handle("/api/v1/dcron/", http.StripPrefix("/api/v1/dcron", dcron.AdminHandler(auth, scheduler)))
```

### Endpoints Reference

| Method | Endpoint Path | Description | Response Status |
| :--- | :--- | :--- | :--- |
| `GET` | `/status` | Returns node ID, namespace, leadership flag, and all job statuses | `200 OK` (JSON) |
| `POST` | `/jobs/{name}/pause` | Pauses future automatic triggers for a specific job | `204 No Content` |
| `POST` | `/jobs/{name}/resume` | Resumes automatic triggers for a previously paused job | `204 No Content` |
| `DELETE` | `/jobs/{name}` | Unregisters a job from the scheduler at runtime | `204 No Content` |

---

### Example REST Calls

#### 1. Cluster Status & Job Snapshot
```bash
curl -s http://localhost:8080/api/v1/dcron/status | jq .
```

**JSON Response:**
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

#### 2. Pause a Job
```bash
curl -X POST http://localhost:8080/api/v1/dcron/jobs/daily-report/pause
```

#### 3. Resume a Job
```bash
curl -X POST http://localhost:8080/api/v1/dcron/jobs/daily-report/resume
```

#### 4. Remove a Job Dynamically
```bash
curl -X DELETE http://localhost:8080/api/v1/dcron/jobs/daily-report
```
