---
id: observability
title: Prometheus Metrics & Tracing
sidebar_label: Observability
---

# 📊 Observability (Prometheus & OpenTelemetry)

`d-cron` provides first-class observability extensions for production telemetry, Grafana dashboards, and distributed tracing.

---

## 📈 Prometheus Metrics Integration

Import the `github.com/mindfiredigital/d-cron/metrics` package to attach Prometheus collectors to standard Go HTTP handlers:

```go
package main

import (
	"net/http"

	"github.com/mindfiredigital/d-cron/dcron"
	"github.com/mindfiredigital/d-cron/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// 1. Create Prometheus Exporter
	promExporter := metrics.NewPrometheusExporter("dcron")

	// 2. Attach Exporter to Scheduler
	scheduler, _ := dcron.New(
		db,
		dcron.WithMetrics(promExporter),
	)

	// 3. Expose /metrics endpoint
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":9090", nil)

	scheduler.Start(context.Background())
}
```

### Exported Prometheus Metrics

| Metric Name | Type | Description | Labels |
| :--- | :--- | :--- | :--- |
| `dcron_is_leader` | Gauge | `1` if this node is active leader, `0` if standby | `instance_id`, `namespace` |
| `dcron_leader_epoch` | Gauge | Current monotonic leader election epoch counter | `instance_id`, `namespace` |
| `dcron_job_executions_total` | Counter | Total number of job execution attempts | `job_name`, `status` (`success`, `failed`, `skipped`) |
| `dcron_job_duration_seconds` | Histogram | Execution latency distribution in seconds | `job_name` |
| `dcron_job_panics_total` | Counter | Total panics caught during job execution | `job_name` |

---

## 🔍 OpenTelemetry (OTel) Distributed Tracing

Trace job runs across your microservice boundary using the OTel tracer provider:

```go
import (
	"github.com/mindfiredigital/d-cron/otel"
)

// Attach OTel Tracer
scheduler, _ := dcron.New(
	db,
	dcron.WithTracer(otel.NewTracer("d-cron")),
)
```

Every job invocation creates an OpenTelemetry span containing:
- `job.name`: String identifier of the job.
- `job.schedule`: Cron expression string.
- `leader.epoch`: Monotonic epoch token.
- `instance.id`: Executing pod ID.
