---
id: metrics
title: Metrics & Telemetry API
sidebar_label: Metrics API
---

# 📊 Metrics & Telemetry API Reference

API reference for optional telemetry adapters in `github.com/mindfiredigital/d-cron/metrics` and `github.com/mindfiredigital/d-cron/otel`.

---

## 📈 `metrics.PrometheusExporter`

```go
type PrometheusExporter struct { ... }

func NewPrometheusExporter(namespace string) *PrometheusExporter
```

Creates a new Prometheus metrics exporter.

### Methods

- `ObserveJobExecution(jobName string, status string, duration time.Duration)`: Records execution duration histogram and status counter.
- `ObservePanic(jobName string)`: Increments panic counter for the specified job.
- `SetLeadership(isLeader bool, epoch int64)`: Updates leadership status gauge and epoch counter.

---

## 🔍 `otel.Tracer`

```go
type Tracer struct { ... }

func NewTracer(serviceName string) *Tracer
```

Creates an OpenTelemetry tracer instance.

### Spans & Attributes

Created spans adopt the naming format `dcron.job.{job_name}` and populate standard semantic attributes:
- `job.name`
- `job.schedule`
- `leader.epoch`
- `instance.id`
