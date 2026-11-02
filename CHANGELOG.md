# Changelog

All notable changes to `d-cron` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v1.1.0] - 2026-10-29

### Added
- **Cluster Mutations with Standby Forwarding**: Distributed admin endpoints (`Pause`, `Resume`, `Remove`, `AddDynamic`) with HTTP proxying from standby replicas to the active leader, backed by durable `job_state` table storage and failover reconciliation.
- **Batched History Pruning**: Added `PruneBatch` for chunked deletion with `LIMIT` and index scans, preventing lock contention and WAL bloat on large execution tables.
- **Epoch Fencing & Split-Brain Containment**: Hardened storage fencing with monotonically increasing database epochs, rejecting stale leader writes on `Record` and `Finish`.
- **Clock Realignment on Promotion**: Added `Queue.Realign` so that upon leadership takeover, jobs configured with `MissedSkip` advance cleanly past standby time without bursting stale missed ticks.
- **Uncooperative Job Timeout**: Immediate timeout reporting in `runAttempt` when a job exceeds its context timeout even if the underlying function ignores context cancellation.
- **Non-blocking Leadership Queries**: Decoupled backend database queries from elector state locks (`acquireMu`), allowing concurrent `IsLeader()` and `Leadership()` inspection without lock starvation.
- **Job LastRun Tracking**: Exported point-in-time `LastRun` execution timestamps in `Jobs() []JobStatus`.
- **Observability Enhancements**: Added `KeyRecordFailures` constant and `HistoryRecorder` interface to observe history persistence errors. Added log throttling for repeated leadership poll errors.
- **CI Hardening & Security**: Pinned Go toolchain versions, added automated CodeQL SAST security analysis, added `godoccheck` documentation coverage gating in CI and `Makefile`, and secured documentation deployment workflows.

### Fixed
- **Kubernetes Example Driver**: Added missing `_ "github.com/lib/pq"` import and dependency to `examples/kubernetes`.
- **Safe Logger Option**: `WithLogger(nil)` safely defaults to `slog.Default()` without causing nil-pointer dereferences.
- **Docstring Corrections**: Corrected misplaced comments on `OverlapPolicy` and `Job`, and resolved obsolete comments in `ui/ui.go`.
- **Race Condition in FinalizeDemotion**: Synchronized state mutations with mutex protection during leader demotion.

---

## [v1.0.2] - 2026-03-15

### Added
- Embedded server-rendered dashboard in `ui` package with zero external asset dependencies.
- OpenTelemetry tracing seam (`otel.Tracer`) and standard trace span generation.
- Prometheus adapter example in `examples/prometheus-adapter`.

### Fixed
- Fixed context cancellation propagation in execution retry loops.

---

## [v1.0.1] - 2026-02-01

### Added
- Metrics recorder seam (`metrics.Recorder`) with standard key definitions.
- Health check endpoints (`sched.HealthCheck`) verifying database lock connection reachability.

### Fixed
- Improved session-stable advisory lock validation on connection pool startup.

---

## [v1.0.0] - 2026-01-10

### Added
- Initial public release of `d-cron`.
- Single-leader distributed cron scheduling using PostgreSQL advisory locks (`pg_try_advisory_lock`).
- Support for standard 5-field cron expressions, 6-field with seconds, and `@every`, `@daily`, `@hourly` descriptors.
- In-process execution with panic isolation and context cancellation.
- Basic execution history logging and execution hooks (`Hook` interface).
