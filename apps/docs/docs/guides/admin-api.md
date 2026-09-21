---
id: admin-api
title: HTTP Admin API & Web UI
sidebar_label: Admin API & Dashboard
---

# 🖥️ HTTP Admin API & Embedded Dashboard

`d-cron` provides an embedded HTTP API and Web UI dashboard to monitor cluster topology, trigger manual job runs, and inspect execution logs.

---

## 🌐 Mounting the Embedded Dashboard UI

Mount the embedded dashboard UI directly onto your standard Go HTTP server using `ui.Handler()`:

```go
package main

import (
	"net/http"

	"github.com/mindfiredigital/d-cron/dcron"
	"github.com/mindfiredigital/d-cron/ui"
)

func main() {
	scheduler, _ := dcron.New(db)

	// Mount Dashboard UI at /dcron/ui/
	http.Handle("/dcron/ui/", http.StripPrefix("/dcron/ui/", ui.Handler(scheduler)))

	log.Println("Admin dashboard running at http://localhost:8080/dcron/ui/")
	http.ListenAndServe(":8080", nil)
}
```

---

## 📡 REST Admin API Endpoints

If you prefer operating custom internal tooling, `d-cron` exposes a standard JSON HTTP Admin API:

```go
adminAPI := dcron.NewAdminAPI(scheduler)
http.Handle("/api/v1/dcron/", http.StripPrefix("/api/v1/dcron", adminAPI.Handler()))
```

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/status` | Returns node leadership status, active leader ID, epoch counter, and uptime. |
| `GET` | `/jobs` | Lists all registered jobs, cron schedules, next run timestamps, and last status. |
| `POST` | `/jobs/{name}/trigger` | Manually triggers immediate execution of a registered job. |
| `POST` | `/jobs/{name}/pause` | Pauses future automatic triggers for a specific job. |
| `POST` | `/jobs/{name}/resume` | Resumes automatic triggers for a paused job. |
| `GET` | `/history` | Returns paginated job execution logs from the history store. |

### Example REST Call: Manual Trigger

```bash
curl -X POST http://localhost:8080/api/v1/dcron/jobs/billing-sync/trigger
```

**JSON Response:**
```json
{
  "status": "success",
  "job": "billing-sync",
  "triggered_at": "2026-09-21T10:00:00Z",
  "leader_epoch": 12
}
```
