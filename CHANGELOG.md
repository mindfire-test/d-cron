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
- **Drain Before Release Option**: Added `WithDrainBeforeRelease` to drain running jobs prior to releasing the advisory lock, preventing cross-replica overlap during node termination.
- **Standby Forwarding Loop Prevention**: Added `X-DCron-Forwarded` header verification returning HTTP 503 when proxy requests loop across standby replicas.
- **Durable Origin Reconciliation**: Tracked `origin` ('static' vs 'dynamic') in `job_state` table so static jobs registered in code are preserved across failovers even if historically removed via admin API.
- **Since-Last-Success Rescheduling**: Reschedules from completion time by default on failure, with optional `WithStopOnFailure` for opt-in halt on error.
- **Asynchronous Missed-Run Catch-Up**: Dispatches catch-up missed runs asynchronously on promotion with per-job timeout budgets, preventing delay to the scheduler's primary clock.
- **Uncooperative Job Overlap Protection**: Holds concurrency locks until late-running goroutines fully exit after timeout, preventing zombie overlap.
- **CI Hardening & Security**: Pinned Go toolchain versions, added automated CodeQL SAST security analysis, added `godoccheck` documentation coverage gating in CI and `Makefile`, and secured documentation deployment workflows.

### Fixed
- **Kubernetes Example Driver**: Added missing `_ "github.com/lib/pq"` import and dependency to `examples/kubernetes`.
- **Safe Logger Option**: `WithLogger(nil)` safely defaults to `slog.Default()` without causing nil-pointer dereferences.
- **Docstring Corrections**: Corrected misplaced comments on `OverlapPolicy` and `Job`, and resolved obsolete comments in `ui/ui.go`.
- **Race Condition in FinalizeDemotion**: Synchronized state mutations with mutex protection during leader demotion.
- **Backend Close Safety**: Managed `stdBackend` close state using `sync.Once` instead of fragile driver error string matching.

---

## [v1.0.2] - 2026-10-21

### Changed
- **Documentation Refinement**: Streamlined documentation structure, updated navigation hierarchy, and removed obsolete section dividers.
- **CI Stability & Tooling Pinning**: Pinned `gofumpt` to `v0.7.0`, `golangci-lint` to `v1.64.8`, and updated `govulncheck` to maintain Go 1.23 compatibility across workflows.

---

## [v1.0.1] - 2026-10-16

### Added
- **Docusaurus Documentation Site**: Full documentation app in `apps/docs` with guides, architecture references, and API specifications.
- **Automated Docs Deployment**: Added `release-docs` workflow to publish documentation to GitHub Pages.

### Performance
- **Optimized Schedule Calculation**: Improved field scanning and bit-level calendar calculations in cron schedule evaluations.

---

## [v1.0.0] - 2026-09-09

### Added
- **Initial Public Release**: Core distributed cron scheduling engine for Go.
- **PostgreSQL Advisory Locks**: Single-leader coordination using session-bound advisory locks (`pg_try_advisory_lock`).
- **Cron Expression Parsing**: Standard 5-field and 6-field (seconds) cron expressions plus `@every`, `@daily`, and `@hourly` descriptors.
- **Execution Engine**: In-process executor with panic containment and context timeout/cancellation.
- **Observability & History**: Pluggable metrics interface, execution hooks, and durable execution history store.
