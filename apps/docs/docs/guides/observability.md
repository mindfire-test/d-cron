---
id: observability
title: Observability & Dashboard
sidebar_label: Observability & Dashboard
---

# Observability, Dashboard & Admin API

`d-cron` provides deep observability across your distributed cron cluster, including an embedded Web Dashboard UI, Prometheus metrics, OpenTelemetry distributed tracing, webhook alert hooks, and an HTTP REST Admin API.

## 1. Embedded Web Dashboard UI

The web dashboard is completely self-contained in `github.com/mindfire-test/d-cron/ui` with zero external CDN dependencies, making it safe for air-gapped production environments.

Mount the dashboard handler on any standard Go HTTP router using `ui.Handler`:

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

	http.Handle("/dcron/ui", ui.Handler(overview, nil))

	scheduler.Start(context.Background())
	log.Println("Dashboard available at http://localhost:8080/dcron/ui")
	http.ListenAndServe(":8080", nil)
}
```

## 2. REST Admin API Endpoints & Security

To programmatically inspect cluster topology or manipulate jobs at runtime, mount `dcron.AdminHandler`:

```go
// Protect admin endpoints with constant-time Bearer token authentication
auth := dcron.RequireBearerToken(os.Getenv("DCRON_ADMIN_TOKEN"))

http.Handle("/api/v1/dcron/", http.StripPrefix("/api/v1/dcron", dcron.AdminHandler(auth, scheduler)))
```

> [!WARNING] Security Warning:
> If `auth` is `nil`, all endpoints are completely unauthenticated. Any client with network reachability to the server can inspect schedules, pause or remove existing jobs, or inject dynamic jobs. In production environments, always provide a non-nil `AuthFunc` (such as `RequireBearerToken`) or place the handler behind an authentication reverse proxy / TLS gateway.

### Standby Mutation Forwarding

In horizontally scaled deployments, clients or dashboards might send HTTP mutation requests (`POST /jobs`, `/pause`, `/resume`, `DELETE`) to any replica behind a round-robin load balancer.

- **With `WithLeaderForwardURL("http://dcron-leader:8080")`**: A standby replica automatically proxies the mutation request to the active leader and streams back the leader's response.
- **Without Leader URL**: Standby replicas reject mutation requests immediately with `409 Conflict` (`dcron: not the leader`), preventing split-brain in-memory edits.

### Endpoints Reference

| Method | Endpoint Path | Description | Response Status |
| :--- | :--- | :--- | :--- |
| `GET` | `/status` | Returns instance ID, namespace, leadership flag, and all job statuses | `200 OK` (JSON) |
| `POST` | `/jobs` | Registers a dynamic job at runtime (`{"name": "...", "spec": "..."}`) | `201 Created` / `409 Conflict` |
| `POST` | `/jobs/{name}/pause` | Pauses future automatic triggers for a specific job | `204 No Content` / `409 Conflict` |
| `POST` | `/jobs/{name}/resume` | Resumes automatic triggers for a previously paused job | `204 No Content` / `409 Conflict` |
| `DELETE` | `/jobs/{name}` | Unregisters and durably removes a job across the cluster | `204 No Content` / `409 Conflict` |

#### Example REST Invocations

```bash
# 1. Cluster Status Snapshot
curl -s -H "Authorization: Bearer secret-token" http://localhost:8080/api/v1/dcron/status | jq .

# 2. Register Dynamic Job at Runtime
curl -X POST -H "Authorization: Bearer secret-token" -H "Content-Type: application/json" \
  -d '{"name":"ad-hoc-sync","spec":"@every 10m"}' \
  http://localhost:8080/api/v1/dcron/jobs

# 3. Pause a Job
curl -X POST -H "Authorization: Bearer secret-token" \
  http://localhost:8080/api/v1/dcron/jobs/daily-report/pause

# 4. Resume a Job
curl -X POST -H "Authorization: Bearer secret-token" \
  http://localhost:8080/api/v1/dcron/jobs/daily-report/resume

# 5. Remove a Job Dynamically
curl -X DELETE -H "Authorization: Bearer secret-token" \
  http://localhost:8080/api/v1/dcron/jobs/daily-report
```

## 3. Prometheus Metrics

`d-cron` defines a pluggable `metrics.Recorder` interface (`github.com/mindfire-test/d-cron/metrics`). Standard metric names are defined as constants so custom Prometheus exporters share a stable contract:

### Standard Metric Keys

| Metric Constant | Metric Name | Type | Description |
| :--- | :--- | :--- | :--- |
| `KeyIsLeader` | `dcron_is_leader` | Gauge | `1` if this replica is the active Leader; `0` if Standby |
| `KeyLeaderTransitions` | `dcron_leader_transitions_total` | Counter | Total leadership promotions and demotions (flapping detector) |
| `KeyJobExecutions` | `dcron_job_executions_total` | Counter | Total job executions partitioned by outcome (`success`, `failed`, `panicked`, `timeout`, `canceled`, `skipped`) |
| `KeyJobDuration` | `dcron_job_duration_seconds` | Histogram | Execution latency distribution per job |
| `KeyJobLastSuccess` | `dcron_job_last_success_timestamp` | Gauge | Unix timestamp of last successful execution per job |
| `KeyJobsRunning` | `dcron_jobs_running` | Gauge | Number of jobs currently executing concurrently |
| `KeyFencedWrites` | `dcron_fenced_writes_total` | Counter | Total database writes rejected due to stale leader epoch (split-brain detector) |
| `KeyMissedRuns` | `dcron_missed_runs_total` | Counter | Total scheduled ticks missed or caught up |
| `KeyRecordFailures` | `dcron_record_failures_total` | Counter | Failed database writes to execution history |
| `KeyBackendErrors` | `dcron_backend_errors_total` | Counter | Database communication failures or advisory lock backend errors |

### Optional Recorder Extensions

Recorders can implement optional extension interfaces:

```go
// HistoryRecorder tracks failures when persisting execution history
type HistoryRecorder interface {
    RecordFailure(job string)
}

// BackendErrorRecorder tracks database or coordination connectivity issues
type BackendErrorRecorder interface {
    BackendError(operation string)
}
```

## 4. Webhook Alerts on Job Outcomes

`d-cron` includes a built-in `WebhookHook` to dispatch JSON POST alerts to Slack, PagerDuty, or Discord:

```go
scheduler, _ := dcron.New(
    db,
    dcron.WithHooks(&dcron.WebhookHook{
        URL:     "https://alerting.internal/api/v1/dcron-events",
        Timeout: 5 * time.Second,
        Headers: map[string]string{
            "Authorization": "Bearer alert-key",
        },
    }),
)
```

**Webhook JSON Payload Schema:**
```json
{
  "job": "billing-settlement",
  "outcome": "failed",
  "attempts": 3,
  "duration_ms": 1520,
  "error": "database connection timeout",
  "time": "2026-10-21T18:00:00Z"
}
```

## 5. OpenTelemetry Distributed Tracing

For distributed tracing across microservice boundaries, implement the `otel.Tracer` interface:

```go
type Tracer interface {
    StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(outcome string, err error))
}
```

Emitted spans automatically attach `job.name`, `attempt`, and the `leader.epoch` fencing token.
