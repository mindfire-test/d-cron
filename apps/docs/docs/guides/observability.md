---
id: observability
title: Observability & Dashboard
sidebar_label: Observability & Dashboard
---

# Observability, Dashboard & Admin API

`d-cron` provides deep observability across your distributed cron cluster, including an embedded Web Dashboard UI, Prometheus metrics, OpenTelemetry distributed tracing, webhook alert hooks, and an HTTP REST Admin API.

---

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

---

## 2. REST Admin API Endpoints

To programmatically inspect cluster topology or manipulate jobs at runtime, mount `dcron.AdminHandler`:

```go
auth := func(r *http.Request) bool {
    // Verify Bearer token or custom header
    return r.Header.Get("Authorization") == "Bearer secret-token"
}

http.Handle("/api/v1/dcron/", http.StripPrefix("/api/v1/dcron", dcron.AdminHandler(auth, scheduler)))
```

### Endpoints Reference

| Method | Endpoint Path | Description | Response Status |
| :--- | :--- | :--- | :--- |
| `GET` | `/status` | Returns node ID, namespace, leadership flag, and all job statuses | `200 OK` (JSON) |
| `POST` | `/jobs/{name}/pause` | Pauses future automatic triggers for a specific job | `204 No Content` |
| `POST` | `/jobs/{name}/resume` | Resumes automatic triggers for a previously paused job | `204 No Content` |
| `DELETE` | `/jobs/{name}` | Unregisters a job from the scheduler at runtime | `204 No Content` |

#### Example REST Invocations

```bash
# 1. Cluster Status Snapshot
curl -s http://localhost:8080/api/v1/dcron/status | jq .

# 2. Pause a Job
curl -X POST http://localhost:8080/api/v1/dcron/jobs/daily-report/pause

# 3. Resume a Job
curl -X POST http://localhost:8080/api/v1/dcron/jobs/daily-report/resume

# 4. Remove a Job Dynamically
curl -X DELETE http://localhost:8080/api/v1/dcron/jobs/daily-report
```

---

## 3. Prometheus Metrics

`d-cron` defines a pluggable `metrics.Recorder` interface (`github.com/mindfire-test/d-cron/metrics`).

### Key Prometheus Metric Descriptors

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `dcron_leadership_status` | Gauge | `namespace`, `node_id` | `1` if this replica is the active Leader; `0` if Standby |
| `dcron_leadership_transitions_total` | Counter | `namespace`, `to_state` | Total leadership promotions and demotions |
| `dcron_job_executions_total` | Counter | `job`, `outcome` | Total execution count partitioned by `success`, `failed`, `skipped`, `panic` |
| `dcron_job_duration_seconds` | Histogram | `job` | Execution latency distribution per job |
| `dcron_job_panics_total` | Counter | `job` | Total caught runtime panics |

### Prometheus Integration Example

```go
type promRecorder struct {
	executions *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	leader     *prometheus.GaugeVec
}

func newPrometheusRecorder(reg prometheus.Registerer) *promRecorder {
	r := &promRecorder{
		executions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dcron_job_executions_total",
			Help: "Total job runs by outcome",
		}, []string{"job", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dcron_job_duration_seconds",
			Help:    "Execution duration in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"job"}),
		leader: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dcron_leadership_status",
			Help: "1 if current node is cluster leader, 0 otherwise",
		}, []string{"namespace"}),
	}
	reg.MustRegister(r.executions, r.duration, r.leader)
	return r
}

func (r *promRecorder) RecordJobExecution(job, outcome string, dur time.Duration) {
	r.executions.WithLabelValues(job, outcome).Inc()
	r.duration.WithLabelValues(job).Observe(dur.Seconds())
}

func (r *promRecorder) RecordLeadershipChange(leader bool) {
	val := 0.0
	if leader {
		val = 1.0
	}
	r.leader.WithLabelValues("orders-service").Set(val)
}
```

---

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

---

## 5. OpenTelemetry Distributed Tracing

For distributed tracing across microservice boundaries, implement the `otel.Tracer` interface:

```go
type Tracer interface {
    StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(outcome string, err error))
}
```

Emitted spans automatically attach `job.name`, `attempt`, and the `leader.epoch` fencing token.
