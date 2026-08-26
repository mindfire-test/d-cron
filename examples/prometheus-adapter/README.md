# prometheus-adapter example

Bridges d-cron's dependency-free `metrics.Recorder` seam to a real Prometheus
registry using `client_golang` (issue #36 / FR-404).

## Why a separate Go module

d-cron's core has **zero third-party dependencies** (NFR-401/402). This
example needs `prometheus/client_golang`, so it carries its own `go.mod` with
a `replace` directive back to the repository root. Building the library or the
other examples never downloads Prometheus.

## Run

```sh
export DATABASE_URL=postgres://localhost:5432/app?sslmode=disable
export HOSTNAME=$(hostname)
go run .            # serves :9090/metrics, scheduler ticks every 5s
curl -s localhost:9090/metrics | grep dcron_
```

## Metric contract

Names come from `metrics.Key*` constants — do not hardcode strings:

| Metric | Type | Labels |
|---|---|---|
| `dcron_is_leader` | gauge | instance |
| `dcron_leader_transitions_total` | counter | instance |
| `dcron_job_executions_total` | counter | job, status |
| `dcron_job_duration_seconds` | histogram | job |
| `dcron_job_last_success_timestamp` | gauge | job |
| `dcron_jobs_running` | gauge | job |
| `dcron_fenced_writes_total` | counter | — |
| `dcron_missed_runs_total` | counter | job |

Suggested alerts (duration-qualified per README §metrics):
- `dcron_is_leader == 0 for 2m` on every replica → election broken.
- `increase(dcron_missed_runs_total[10m]) > 0` → capacity problem.
- `increase(dcron_fenced_writes_total[5m]) > 0` → split-brain signal; page.
