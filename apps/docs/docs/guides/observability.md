---
id: observability
title: Observability (Prometheus & OpenTelemetry)
sidebar_label: Observability
---

# 📊 Observability (Prometheus & OpenTelemetry)

`d-cron` provides a dependency-free observability architecture. The core scheduler never imports heavy metrics SDKs, maintaining the zero-unnecessary-dependencies guarantee.

Instead, the core emits observability signals through the clean `metrics.Recorder` interface, which your application wires into your own Prometheus, StatsD, or OpenTelemetry registry.

---

## 📈 Prometheus Metrics Integration

Implement `metrics.Recorder` (or embed `metrics.Noop` and override what you need) to expose Prometheus metrics:

```go
package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type prometheusRecorder struct {
	metrics.Noop
	isLeader   *prometheus.GaugeVec
	executions *prometheus.CounterVec
	duration   *prometheus.HistogramVec
}

func newPrometheusRecorder(reg prometheus.Registerer) *prometheusRecorder {
	r := &prometheusRecorder{
		isLeader: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.KeyIsLeader,
			Help: "1 if this node holds leadership, 0 if standby",
		}, []string{"instance"}),
		executions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metrics.KeyJobExecutions,
			Help: "Total job executions by status",
		}, []string{"job", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: metrics.KeyJobDuration,
			Help: "Job execution duration in seconds",
		}, []string{"job"}),
	}
	reg.MustRegister(r.isLeader, r.executions, r.duration)
	return r
}

func (r *prometheusRecorder) SetLeader(instance string, isLeader bool) {
	val := 0.0
	if isLeader {
		val = 1.0
	}
	r.isLeader.WithLabelValues(instance).Set(val)
}

func (r *prometheusRecorder) JobFinished(job string, outcome metrics.Outcome, dur time.Duration, success bool) {
	r.executions.WithLabelValues(job, outcome.String()).Inc()
	r.duration.WithLabelValues(job).Observe(dur.Seconds())
}

func main() {
	db, _ := sql.Open("postgres", "postgres://user:pass@localhost:5432/myapp?sslmode=disable")

	promRec := newPrometheusRecorder(prometheus.DefaultRegisterer)

	scheduler, _ := dcron.New(
		db,
		dcron.WithNamespace("orders-service"),
		dcron.WithSessionStableConnection(),
		dcron.WithMetrics(promRec),
	)

	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":9090", nil)

	scheduler.Start(context.Background())
}
```

---

## 🔔 Webhook Alerts on Job Outcomes

`d-cron` includes a built-in `WebhookHook` to dispatch JSON POST payloads to Slack, Discord, or monitoring alerts:

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

## 🔍 OpenTelemetry (OTel) Distributed Tracing

For distributed tracing across microservice boundaries, implement the `otel.Tracer` interface:

```go
type Tracer interface {
    StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(outcome string, err error))
}
```
Emitted spans capture `job.name`, `attempt`, and the `leader.epoch` fencing token.
