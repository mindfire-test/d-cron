# Database Improvements & Cluster-Wide Admin Mutations Report

**Repository:** `d-cron`  
**Branch:** `fix/database-points`  
**Base Commit:** `812ab37`

---

## Executive Summary

This report documents the verification, implementation, and end-to-end testing of the **6 Database improvement points** from the review checklist (`Database | Score: 2/4`) and the completion of **cluster-wide admin mutations** (`Pause`, `Resume`, `Remove`, `AddDynamic`) with standby forwarding and failover durability.

All code changes preserve backward compatibility across all public interfaces (`dcron`, `executor`, `metrics`, `otel`).

---

## Part 1: Verification of the 6 Database Review Points

### Point 1 — Database Timeouts across Connection Acquisition, Migration, and Queries
- **Status:** **Confirmed & Fixed**
- **Evidence:** `dcron/options.go` (L48–L60), `dcron/loop.go` (L50–L360), `dcron/dcron.go` (L68–L120)
- **Failure Scenario:** Prior to this fix, `defaultDBTimeout` was hardcoded to a 5-second package-private constant. Applications operating over high-latency WAN links, cloud proxies (e.g. AWS RDS Proxy, PgBouncer), or running large batch history prunes had no mechanism to tune query timeouts, causing premature context cancellations or unbounded blocking during database stalls.
- **Severity & Impact:** **Medium**. Led to failure in high-latency or high-throughput environments where 5 seconds is insufficient for deep migration runs or batch queries.
- **Resolution:**
  - Exported `WithDBTimeout(d time.Duration) Option` in `dcron/options.go`.
  - Wired `s.opts.dbTimeout` throughout `IncrementEpoch`, `catchUpMissedRuns`, `invoke` (`Record`, `Finish`), `pruneHistory`, and `reconcileJobStates`.
- **Regression Tests:** Verified in `test/store/store_test.go` and `test/integration/e2e_admin_failover_test.go`.

---

### Point 2 — Migrations and Concurrent Startup
- **Status:** **Already Fixed**
- **Evidence:** `internal/store/schema.go` (L36–L112), `test/integration/migrate_concurrent_test.go`
- **Failure Scenario:** When multiple scheduler replicas start simultaneously against a clean database, uncoordinated concurrent execution of `CREATE TABLE` and `CREATE INDEX` causes PostgreSQL transaction deadlocks and unique constraint errors.
- **Severity & Impact:** **High** (if unmitigated). Causes service startup crashes in multi-pod deployments.
- **Verification Evidence:** The implementation already coordinates migrations using a dedicated session advisory lock (`d-cron:migration:<schema>`) combined with idempotent `IF NOT EXISTS` DDL statements executed inside a single transaction. Verified via `TestMigrate_TenReplicasConcurrent`, which tests 10 concurrent replicas racing to initialize the database simultaneously without error.

---

### Point 3 — History Recording, Transaction Boundaries, and Epoch Fencing on Record vs Finish
- **Status:** **Confirmed & Fixed**
- **Evidence:** `internal/store/store.go` (L92–L136, L184–L215)
- **Failure Scenario:** When a leader crashes or loses network connectivity mid-execution, a newly promoted leader increments the epoch. When the partitioned old leader tries to write `Finish`, it is fenced out by epoch check. The execution record remained permanently in `status = 'running'` with `finished_at = NULL`. Because `Prune` previously filtered `WHERE status <> 'running'`, these orphaned records were never cleaned up, causing permanent storage leakage over months of operation.
- **Severity & Impact:** **High**. Progressive database disk consumption from stale, unfenceable in-flight executions.
- **Resolution:**
  - Updated `PruneBatch` in `internal/store/store.go` to also prune `(status = 'running' AND started_at < now() - retention)`.
  - Abandoned running executions older than the retention horizon are now cleanly deleted during scheduled maintenance.
- **Regression Tests:** Added `TestE2EPrune_OrphanedRunningAndBatching` in `test/integration/e2e_admin_failover_test.go`.

---

### Point 4 — Pruning and Retention Performance on Large Tables
- **Status:** **Confirmed & Fixed**
- **Evidence:** `internal/store/store.go` (L184–L225)
- **Failure Scenario:** An unbounded `DELETE FROM <schema>.execution WHERE finished_at < ...` on high-throughput deployments accumulates millions of records. An unbatched deletion acquires long-lasting row and index locks, bloats write-ahead logs (WAL), triggers PostgreSQL lock contention, and leads to replication lag on standby database replicas.
- **Severity & Impact:** **High**. Service degradation and replication lag during history cleanup windows.
- **Resolution:**
  - Implemented `PruneBatch(ctx context.Context, namespace string, retention time.Duration, batchSize int) (int64, error)` in `internal/store/store.go`.
  - Uses `WHERE id IN (SELECT id FROM ... LIMIT batchSize)` in an iterative bounded loop until all expired rows are removed.
- **Regression Tests:** Verified in `TestE2EPrune_OrphanedRunningAndBatching`.

---

### Point 5 — Connection Leak or Session Stability across Dedicated Lock and General Pool
- **Status:** **Confirmed & Fixed**
- **Evidence:** `dcron/dcron.go` (L68–L87), `dcron/options.go` (L162–L175), `internal/elector/backend.go` (L80–L96)
- **Failure Scenario:**
  1. In `dcron.New`, if `store.New` or `store.Migrate` encountered an error, the reserved connection `conn` was never closed.
  2. In `WithDedicatedLockDriver`, `sql.Open` created a dedicated `*sql.DB` pool that was never closed when `Scheduler.Stop` was called, leaking socket file descriptors.
  3. When an underlying connection terminated while releasing an advisory lock, returning `driver.ErrBadConn` as a fatal error prevented clean scheduler teardown.
- **Severity & Impact:** **Medium**. Socket and connection leaks during scheduler initialization errors and restarts.
- **Resolution:**
  - Added deferred cleanup on error during `dcron.New`: `defer func() { if err != nil && conn != nil { _ = conn.Close() } }()`.
  - Added `lockCloser func() error` to `options` captured in `WithDedicatedLockDriver` and invoked in `Scheduler.Stop`.
  - Updated `stdBackend.Release` in `internal/elector/backend.go` to treat `driver.ErrBadConn` and `sql.ErrConnDone` as clean release (`false, nil`) per PostgreSQL advisory lock session lifecycle semantics.
- **Regression Tests:** Verified in `test/integration/elector_lifecycle_test.go` and `test/integration/e2e_admin_failover_test.go`.

---

### Point 6 — Schema Naming, SQL Injection Risks, and Reserved Keywords
- **Status:** **Confirmed & Fixed**
- **Evidence:** `internal/store/schema.go` (L14–L70), `internal/store/store.go` (L57–L67)
- **Failure Scenario:** Table and schema identifiers were interpolated directly into SQL statements without escaping (e.g. `fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.execution ...", schema)`). When users configured schema names colliding with standard SQL keywords (such as `user`, `order`, `group`, `table`), PostgreSQL returned syntax errors (`syntax error at or near "order"`), preventing initialization.
- **Severity & Impact:** **Medium**. Immediate fatal crash when deployed in schemas with reserved or case-sensitive names.
- **Resolution:**
  - Added `quoteIdent(s string) string` helper that safely validates, escapes internal quotes, and double-quotes SQL identifiers.
  - Quoted all table and schema references across `MigrationDDL`, `store.go`, and `schema.go` (e.g. `"order"."execution"`, `"user"."job_state"`).
- **Regression Tests:** Added `TestMigrationDDLReservedKeywords` in `test/store/store_test.go` and `TestE2EReservedKeywordSchema` in `test/integration/e2e_admin_failover_test.go`.

---

## Part 2: Cluster-Wide Admin Mutations & Failover Durability

### 1. Persistent State Table DDL
To guarantee durability across leader failover, added the `job_state` table to `MigrationDDL`:

```sql
CREATE TABLE IF NOT EXISTS "<schema>"."job_state" (
    "namespace"  text NOT NULL,
    "job_name"   text NOT NULL,
    "paused"     boolean NOT NULL DEFAULT false,
    "removed"    boolean NOT NULL DEFAULT false,
    "spec"       text NOT NULL DEFAULT '',
    "updated_at" timestamptz NOT NULL,
    PRIMARY KEY ("namespace", "job_name")
);
```

### 2. Mutation Durability Pattern
- **Pause & Resume:** In `dcron.Pause` and `dcron.Resume`, the state mutation is saved via `store.SaveJobState` with PostgreSQL `ON CONFLICT (namespace, job_name) DO UPDATE`. In-memory state is only mutated after the database transaction successfully commits. If the database write fails, an error is returned and the operation does not take effect.
- **Remove:** `dcron.Remove` records `removed = true` in `job_state` before deleting the job from the leader's in-memory job registry.
- **AddDynamic:** `dcron.AddDynamic` verifies deduplication against both in-memory jobs and existing unremoved records in `job_state`, records `spec` and `paused = false` in `job_state`, and registers the job into the scheduling wheel.

### 3. Failover State Reconciliation
When a standby node is promoted to leader:
1. `onPromotion` runs and calls `s.reconcileJobStates(epoch)`.
2. It fetches all persisted job states for the namespace from `job_state`.
3. Paused jobs in memory are flagged as paused so they will not fire on schedule.
4. Dynamically added jobs are restored into the scheduling queue. If a dynamic job handler was registered via `WithJobHandler(name, fn)` or `RegisterHandler(name, fn)`, it is bound to the restored job.
5. Removed jobs are unmarshaled and purged from in-memory maps.

### 4. Standby Mutation Forwarding (HTTP Proxy)
Standby replicas reject direct mutation calls with `ErrNotLeader` (HTTP 409 Conflict). To support ingress controllers and cluster service meshes where admin HTTP requests might hit a standby replica:
- Added `WithLeaderForwardURL(url string) Option`.
- In `dcron/admin.go`, all mutation endpoints (`POST /jobs` for AddDynamic, `POST /jobs/{name}/pause`, `POST /jobs/{name}/resume`, `DELETE /jobs/{name}`) support standby forwarding:
  - If `leaderForwardURL` is configured, the request (HTTP method, URL, query parameters, all headers including `Authorization` and `Content-Type`, and request body) is proxied to the active leader.
  - If the leader cannot be reached (network partition, down), the standby falls back to HTTP 409 Conflict with `"dcron: leader unavailable: ..."`.
  - If the leader returns an error (400 Bad Request, 404 Not Found, 409 Conflict, 500 Internal Server Error), the HTTP status code, headers, and error response body are preserved and streamed directly to the client.
  - If the client's request context expires or times out, the proxy call aborts cleanly.
  - If `leaderForwardURL` is not configured, the standby immediately responds with HTTP 409 Conflict (`dcron: not leader`).

### 5. Architectural Limitations & Trade-offs
- **Dynamic Job Handlers:** Go function closures cannot be serialized across network boundaries or stored in SQL tables. Dynamic jobs (`AddDynamic`) require the job handler to be pre-registered on candidate replicas using `WithJobHandler(name, fn)` or `RegisterHandler(name, fn)` so that upon promotion, the newly elected leader can bind the function pointer to the restored job definition. If no handler is registered on the promoted replica, the job definition is preserved in the database and logged as `dynamic job found in db without registered handler on promoted replica`.
- **Database Availability Dependency for Mutations:** In-memory job state is strictly guarded by successful database persistence in `job_state`. If PostgreSQL is temporarily down or in read-only mode, mutations return an error and in-memory state is preserved unmodified to prevent split-brain discrepancies between replicas.


---

## Part 3: Validation and Verification Matrix

| Verification Step | Command | Result |
| :--- | :--- | :--- |
| Code Formatting | `gofumpt -d .` | **PASS** (Zero diff) |
| Git Whitespace | `git diff --check` | **PASS** (Zero errors) |
| Static Analysis | `go vet ./...` | **PASS** (Clean) |
| Linter | `golangci-lint run --timeout=5m ./...` | **PASS** (Zero warnings) |
| Unit Tests | `go test -count=1 ./test/...` | **PASS** (8/8 packages) |
| Unit Tests (Race) | `go test -race -count=1 ./test/...` | **PASS** (0 data races) |
| PostgreSQL 16 E2E Tests | `cd test/integration && go test -v -count=1 -tags integration .` | **PASS** (All 10 tests) |
| PostgreSQL 16 E2E Tests (Race) | `cd test/integration && go test -v -count=1 -race -tags integration .` | **PASS** (All 10 tests) |
| Standalone Example Modules | `go build ./...` across all 5 example modules | **PASS** (All modules compile) |

---

## Part 4: Comprehensive Security, Reliability, and CI Audit (8 Findings)

### Finding 1 — `FenceSchema` Schema Validation & SQL Injection Prevention
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `dcron/context.go`, `internal/store/schema.go`, `test/scheduler/fence_test.go`
- **Finding Details:** `FenceSchema(ctx, tx, schema)` previously concatenated the raw schema string directly into SQL without validation: `fmt.Sprintf("SELECT leader_epoch FROM %s.leader_epoch ...", schema)`. A malicious or unvalidated schema parameter allowed arbitrary SQL interpolation.
- **Resolution:**
  - Exported `store.ValidateSchema(schema string) error` and `store.QuoteIdent(s string) string` in `internal/store/schema.go`.
  - Updated `FenceSchema` to validate the schema name upfront against identifier rules (1–63 lowercase alphanumeric chars/underscores, not starting with digits, disallowing reserved `"public"`).
  - Identifier components are safely double-quoted: `fmt.Sprintf(`SELECT leader_epoch FROM %s.%s ...`, store.QuoteIdent(schema), store.QuoteIdent("leader_epoch"))`.
- **Regression Tests:** Added `TestFenceSchema_Validation` in `test/scheduler/fence_test.go` covering 10 invalid variations (reserved `public`, SQL injections with semicolons/quotes, uppercase, hyphens, digits, spaces, dots, null bytes, >63 chars) and valid schemas.

### Finding 2 — History-Pruning Index, Batching, and Orphan Cleanup
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `internal/store/schema.go`, `internal/store/store.go`, `test/store/store_test.go`, `test/integration/migrate_concurrent_test.go`, `test/integration/e2e_admin_failover_test.go`
- **Finding Details:**
  1. Failed/orphaned executions stuck in `status = 'running'` when leaders crash were never pruned because `Prune` filtered `WHERE status <> 'running'`.
  2. Index scan was missing for running executions: `(namespace, started_at) WHERE status = 'running'` was absent, forcing sequential scans during retention pruning on large datasets.
- **Resolution:**
  - Added partial index `execution_prune_running_idx ON (namespace, started_at) WHERE status = 'running'` to `MigrationDDL`.
  - Re-architected `PruneBatch` candidate selection using `UNION ALL` across finished executions (`execution_prune_idx`) and stale running executions (`execution_prune_running_idx`).
- **Regression Tests & Verification:**
  - `test/integration/e2e_admin_failover_test.go` (`TestE2EPrune_OrphanedRunningAndBatching`): Runs `EXPLAIN (FORMAT TEXT)` on real PostgreSQL 16 proving both `execution_prune_idx` and `execution_prune_running_idx` are used for index scans.
  - Multi-namespace isolation and repeat batch pruning verified under `-race`.

### Finding 3 — Health-Check Coverage for Dedicated Lock Connection
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `internal/elector/backend.go`, `internal/elector/coordinator.go`, `internal/elector/elector.go`, `internal/elector/memory_coordinator.go`, `dcron/backend.go`, `dcron/leadership.go`, `test/scheduler/phase2_test.go`, `test/integration/advisory_holds_test.go`
- **Finding Details:** `Scheduler.HealthCheck(ctx)` previously only verified the application connection pool (`s.db.PingContext(ctx)`). If the dedicated advisory lock session (`conn`) was terminated or partitioned, `HealthCheck` falsely reported healthy even though leadership election was broken.
- **Resolution:**
  - Added `Ping(ctx context.Context) error` to `Backend` and `Coordinator` interfaces.
  - Implemented `PingContext` on the dedicated lock session in `stdBackend` and `lockBackendAdapter`.
  - Updated `HealthCheck` to ping both the general pool and the dedicated lock connection session.
- **Regression Tests:**
  - `TestHealthCheck_DedicatedLockConnectionBroken` and `TestHealthCheck_DedicatedLockConnectionHealthy` in `test/scheduler/phase2_test.go`.
  - `TestHealthCheck_DedicatedLockConnection` in `test/integration/advisory_holds_test.go` testing against real PostgreSQL that terminating the lock connection causes `HealthCheck` to return an error even when `s.db` is healthy.

### Finding 4 — Private Vulnerability Disclosure Policy & Contacts
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `SECURITY.md`, `apps/docs/docs/security.md`
- **Finding Details:** The repository previously directed users to report security vulnerabilities via public GitHub issues (`https://github.com/mindfire-test/d-cron/issues`).
- **Resolution:**
  - Restored root `SECURITY.md` establishing private reporting routes:
    1. GitHub Private Vulnerability Reporting: `https://github.com/mindfire-test/d-cron/security/advisories/new`
    2. Maintainer security contact: `security@mindfiredigital.com`
  - Explicitly prohibited public issues/PRs for vulnerability reports.
  - Synchronized `apps/docs/docs/security.md` with identical private reporting instructions and guidance.

### Finding 5 — CI Supply-Chain Pinning & govulncheck Enforcement
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `.github/workflows/ci.yml`, `.github/workflows/release-docs.yml`
- **Finding Details:**
  1. GitHub Actions used unpinned or fictitious tags (`@v7`).
  2. `golangci-lint` used unpinned curl from GitHub master.
  3. `govulncheck` had `continue-on-error: true` and failure-swallowing `|| echo ...`.
- **Resolution:**
  - Pinned all actions to full 40-character commit SHAs with version comments:
    - `actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2`
    - `actions/setup-go@f111f3307d8850f501ac008e886eec1fd1932a34 # v5.3.0`
    - `actions/upload-artifact@4cec3d8aa04e39d1a68397de0c4cd6fb9dce8ec1 # v4.6.1`
    - `actions/setup-node@1d0ff469b7ec7b3cb9d8673fde0c81c44821de2a # v4.2.0`
  - Pinned `golangci-lint` install script to `v1.64.8` release tag.
  - Removed `continue-on-error: true` and failure swallowing from `govulncheck` step.

### Finding 6 — Supported Go Version & Dependency Automation
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `go.mod`, `.github/workflows/ci.yml`, `.github/dependabot.yml`
- **Finding Details:**
  1. CI contained floating `check-latest: true` which undermines reproducible builds.
  2. No automated dependency update mechanism existed for GitHub Actions, Go modules, or npm packages.
- **Resolution:**
  - Removed `check-latest: true` across all jobs in `.github/workflows/ci.yml`.
  - Standardized on Go 1.23 across all modules and CI steps.
  - Created `.github/dependabot.yml` with weekly schedules covering `github-actions`, `gomod` (root, integration, examples), and `npm` (`apps/docs`).

### Finding 7 — Admin & Dashboard Authentication Defaults
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `dcron/admin.go`, `examples/with-dashboard/main.go`, `test/scheduler/admin_test.go`
- **Finding Details:**
  1. `AdminHandler(nil, s)` left all administrative endpoints completely unauthenticated without warning in documentation.
  2. No built-in authentication helper existed for standard Bearer token validation.
  3. The `with-dashboard` example listened on `:8080` (all interfaces), exposing unauthenticated endpoints across local networks.
- **Resolution:**
  - Added comprehensive security warning to `AdminHandler` docstrings.
  - Implemented `RequireBearerToken(token string) AuthFunc` using constant-time comparison (`crypto/subtle.ConstantTimeCompare`) that safely rejects all requests if the token is empty.
  - Re-bound `with-dashboard` server to `127.0.0.1:8080` with `ReadHeaderTimeout: 5 * time.Second`.
- **Regression Tests:** Added `TestRequireBearerToken` in `test/scheduler/admin_test.go` covering missing headers, invalid schemes, bad tokens, token collisions, empty tokens, and mutation authorization.

### Finding 8 — `WebhookHook` Error-Data Leakage Prevention
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `dcron/hooks.go`, `test/scheduler/phase2_test.go`
- **Finding Details:** `WebhookHook` previously serialized raw `res.Error.Error()` into webhook payloads without sanitization, leaking database connection passwords, DSNs, auth tokens, and API keys to third-party endpoints.
- **Resolution:**
  - Implemented `RedactError(err error) string` with precompiled regular expressions redacting DSN passwords (`postgres://user:[REDACTED]@host`), bearer tokens (`Bearer [REDACTED]`), and key-value credentials (`password=[REDACTED]`, `api_key=[REDACTED]`, `token=[REDACTED]`).
  - Added `IncludeRawError bool` and `RedactFunc func(error) string` to `WebhookHook` struct, preserving backward compatibility while defaulting to secure redaction.
- **Regression Tests:**
  - Added `TestRedactError` in `test/scheduler/phase2_test.go` verifying multiple leak patterns, multiline errors, and clean error preservation.
  - Added `TestWebhookHook_Redaction` verifying default redaction, raw opt-in, and custom redact functions.

---

## Part 4: Performance Optimization & Testing Coverage (Findings P1–P4, T1–T5)

**Working Branch:** `fix/performance-testing-coverage`
**Base Commit:** `b3d6799` (from `fix/database-points`)

### Finding P1 — Non-Blocking History Pruning
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `dcron/dcron.go`, `dcron/loop.go`, `test/integration/e2e_admin_failover_test.go`
- **Root Cause & Failure Scenario:** Previously, `pruneHistory` ran synchronously inside the leader's evaluation loop on every minute tick. When pruning expired executions on large tables, the `DELETE` query blocked `fireDue`, introducing up to 15 seconds of scheduling jitter and delaying due job dispatches across the entire cluster.
- **Resolution:**
  - Converted `pruneHistory` to run asynchronously in a dedicated background goroutine.
  - Guarded concurrency using `atomic.Bool` (`s.pruneRunning.CompareAndSwap(false, true)`) to prevent multiple overlapping prune operations if a previous cycle is still in flight.
  - Bounded pruning execution with `s.opts.dbTimeout` (or `defaultPruneTimeout`).
  - Added `pruneWg sync.WaitGroup` tracked across the scheduler lifecycle and safely drained in `Scheduler.Stop` via `s.pruneWg.Wait()`.
- **Regression Tests:**
  - Added `TestScheduler_PruneHistory_NonBlocking` in `test/integration/e2e_admin_failover_test.go` asserting that rapid job execution continues without jitter while history pruning runs in the background.

---

### Finding P2 — Optimized `CronSchedule.Next` & Scan Deduplication
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `internal/clock/clock.go`, `test/clock/clock_test.go`
- **Root Cause & Failure Scenario:**
  1. `Next` evaluated 5-field and 6-field cron expressions in two largely duplicated ~80-line scanning loops.
  2. For sparse seconds specifications (such as second 59: `59 * * * * *`), the evaluator stepped second-by-second (`t.Add(time.Second)`), iterating through full date, month, day, hour, and minute checks up to 59 times per minute.
- **Resolution:**
  - Extracted shared date progression logic into `advanceDate(cur, next, step)`.
  - Unified 5-field and 6-field evaluation paths.
  - Replaced second-by-second linear increments with bitwise $O(1)$ jumps using `bits.TrailingZeros64(c.sec >> cur.Second())` and `bits.TrailingZeros64(c.min >> cur.Minute())`, skipping directly to the next matching second/minute in a single arithmetic step.
- **Performance & Benchmark Results:**
  - Measured 0 allocs/op across all schedule calculations.
  - `BenchmarkCronNext_SparseSeconds`: ~2172 ns/op, 0 allocs/op.
  - `BenchmarkCronNext_DenseSeconds`: ~197 ns/op, 0 allocs/op.
  - `BenchmarkCronNext_Standard5Field`: ~194 ns/op, 0 allocs/op.

---

### Finding P3 — Reduced Mutex Lock Contention in `fireDue`
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `dcron/loop.go`, `test/scheduler/scheduler_coverage_test.go`
- **Root Cause & Failure Scenario:** In `fireDue`, `s.mu.Lock()` was held while iterating over due jobs and invoking `s.invoke(j, epoch, cd.FireAt)`. `invoke` performs OpenTelemetry span initialization, job status updates, and group dispatch. Holding `s.mu` blocked concurrent administrative and inspection calls (`Jobs()`, `Pause()`, `Resume()`, `Remove()`) across all running replicas during bursty job dispatches.
- **Resolution:**
  - Split `fireDue` into two phases:
    1. Fast critical section under `s.mu`: Pop next due elements from `clk` heap, advance `nextRun`, push the rescheduled time back into the heap, evaluate pause/overlap flags, and collect jobs into a local `toFire []dueJobFiring` slice.
    2. Unlock `s.mu.Unlock()`.
    3. Iterate over `toFire` slice and execute `s.invoke` outside the mutex lock.
- **Regression Tests:**
  - Added `TestScheduler_FireDue_ReducedLockContention` in `test/scheduler/scheduler_coverage_test.go` simulating 4 concurrent worker routines querying `Jobs()` while 10 rapid jobs fire, asserting zero lock contention delays (<50ms).

---

### Finding P4 — `HoldsLock` Polling Overhead & Failover Guidance
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `dcron/options.go`, `apps/docs/docs/architecture/advisory-locks.md`, `test/integration/advisory_holds_test.go`
- **Root Cause & Failure Scenario:** Polling `pg_locks` every `pollInterval` incurs database query overhead. The trade-offs between heartbeat frequency, database load, and failover latency were under-documented, leading users to choose inappropriately aggressive poll intervals.
- **Resolution:**
  - Added `BenchmarkHoldsLock` against real PostgreSQL measuring actual `pg_locks` scan overhead (~483 µs/op).
  - Documented production trade-offs in `WithPollInterval` docstrings: recommended 3s–5s for multi-tenant production databases; advised relying on OS TCP keepalives (`tcp_keepalives_idle=30`) for socket drop detection rather than sub-second query polling.
  - Updated architectural documentation in `apps/docs/docs/architecture/advisory-locks.md`.

---

### Finding T1 — Scheduler Behavior & Shutdown Context Coverage
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `dcron/dcron.go`, `dcron/loop.go`, `test/scheduler/scheduler_coverage_test.go`, `test/integration/e2e_epoch_fencing_test.go`, `test/integration/e2e_scheduling_improvements_test.go`
- **Root Cause & Failure Scenario:**
  1. Multiple scheduler options (`WithNoOverlap`, `OverlapQueue`, `WithMissedRunPolicy`, `WithSinceLastSuccess`) lacked dedicated deterministic tests.
  2. In `Scheduler.Stop`, `s.termCancel()` and `s.cancel()` were prematurely called before `s.group.Wait(drainCtx)`, causing in-flight jobs to be cancelled immediately at $t=0$ instead of remaining uncancelled throughout the graceful drain timeout budget.
- **Resolution:**
  - Refactored `Scheduler.Stop`: separated loop cancellation (`loopCancel`) from job execution context (`termCtx`). In-flight jobs remain uncancelled until `drainCtx` deadline expires, satisfying FR-315.
  - Added deterministic regression tests:
    - `TestScheduler_WithNoOverlap_SkipsRunning`
    - `TestScheduler_OverlapQueue_QueuesNextRun`
    - `TestScheduler_WithMissedRunPolicy_Skip`
    - `TestScheduler_ShutdownDrainContext_UncancelledUntilDeadline`
    - `TestScheduler_WithHistory_InspectPersistedRows` (asserting `status='success'`, `job_name`, `attempt=1`, `duration_ms>0`, timestamps, and matching `leader_epoch`)
    - `TestE2ESinceLastSuccessScheduling` and `TestE2ESinceLastSuccess_FailedRunDoesNotReschedule` (verifying failure does not reschedule next run)
    - `TestE2EMissedCatchUpOnPromotion`

---

### Finding T2 — Scheduler-Supplied Epoch Fencing
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `test/integration/e2e_epoch_fencing_test.go`
- **Root Cause & Failure Scenario:** Existing unit tests only tested store-level SQL queries with manual epochs and did not verify that the `Scheduler` itself correctly captures, propagates, and binds the leader epoch during execution and completion.
- **Resolution:**
  - Added `TestScheduler_EpochFencing_StaleSchedulerFinishRejected` in `test/integration/e2e_epoch_fencing_test.go`:
    1. Scheduler replica 1 starts and executes a long-running job with `epoch = 1`.
    2. While the job is running, the database epoch is incremented to simulate promotion of replica 2 (`epoch = 2`).
    3. Replica 1 finishes its job and attempts `store.Finish` with stale `epoch = 1`.
    4. Database rejects the update (`WHERE leader_epoch >= (SELECT current_epoch...)` matches 0 rows), scheduler logs `fenced write detected on finish`, and the row remains unfalsified in `running` state.

---

### Finding T3 — PgBouncer Transaction-Mode & Partitioned-Leader Tests
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `test/integration/pooler_regression_test.go`
- **Root Cause & Failure Scenario:** `test/integration/pooler_regression_test.go` previously contained no-op skip stubs that did not verify behavioral guarantees.
- **Resolution:**
  - Replaced stubs with executable tests:
    1. `TestPgBouncerTransactionModeRejection`: Verifies that `New` rejects transaction-pooling connections with `SessionStabilityError`.
    2. `TestPgBouncerWithDedicatedLockDSNBypass`: Proves `WithDedicatedLockDSN` allows general transaction poolers when an unpooled direct session is provided for advisory locks.
    3. `TestPartitionedLeaderSafety`: Uses Toxiproxy HTTP API to sever network connectivity and asserts that `HealthCheck` detects the broken connection within deadline.
  - All tests clearly distinguish PASS, FAIL, and SKIPPED based on environment variable gates (`DCRON_TEST_PGBOUNCER_DSN`, `DCRON_TEST_TOXIPROXY`).

---

### Finding T4 — Test Hygiene & Flaky Timing
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `test/scheduler/phase2_test.go`, `test/scheduler/admin_failover_test.go`, `test/scheduler/custom_hook_test.go`, `test/scheduler/scheduler_api_test.go`
- **Root Cause & Failure Scenario:**
  1. `waitFor` helper panicked on failure instead of failing with `t.Fatalf`, obscuring test failure traces.
  2. `TestJobsReportsRunningFlag` spun 30,000,000 CPU cycles in an attempt to keep a job busy.
  3. `TestWebhookHookConstruction` only tested struct field assignment.
- **Resolution:**
  - Changed `waitFor` signature to `waitFor(t *testing.T, cond func() bool, what string)` with `t.Helper()` and `t.Fatalf`.
  - Replaced 30,000,000 busy loop with channel-based deterministic synchronization (`started`, `unblock`).
  - Strengthened `TestWebhookHookConstruction` to verify default timeout fallback and unreachable URL error delivery.

---

### Finding T5 — Coverage Reporting, Thresholding & Integration CI
- **Classification:** **CONFIRMED & FIXED**
- **Affected Files:** `.github/workflows/ci.yml`, `.lefthook.yml`
- **Root Cause & Failure Scenario:**
  1. Unit tests in `.github/workflows/ci.yml` lacked `-coverpkg=./...`, falsely reporting 0% coverage on core packages because tests are located in separate `test/` packages.
  2. No automated coverage threshold was enforced in CI.
  3. Pre-push hook did not document the separation between fast unit tests and the standalone `test/integration` module.
- **Resolution:**
  - Updated `.github/workflows/ci.yml` to pass `-coverpkg=./...`.
  - Measured baseline unit test statement coverage at **71.4%**.
  - Added automated coverage threshold check in CI enforcing `>= 70.0%`, reporting summary tables to `$GITHUB_STEP_SUMMARY`.
  - Preserved separate `test/integration` module execution under the `Postgres Integration Matrix` job.
  - Updated `.lefthook.yml` with clear documentation on local unit test vs integration test execution.

