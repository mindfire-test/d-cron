# Software Requirements Specification

## `d-cron` — Distributed Cron for Horizontally-Scaled Go Applications

| Field | Value |
| :--- | :--- |
| Document ID | DCRON-SRS-001 |
| Version | 1.0 (Draft) |
| Date | 2026-08-13 |
| Status | For Review |
| Author | Mindfire — Golang Practice |
| License (project) | Apache-2.0 (proposed) |

---

## Table of Contents

1. [Introduction](#1-introduction)
2. [Overall Description](#2-overall-description)
3. [Functional Requirements](#3-functional-requirements)
4. [Non-Functional Requirements](#4-non-functional-requirements)
5. [Constraints and Assumptions](#5-constraints-and-assumptions)
6. [Correctness Model](#6-correctness-model)
7. [Out of Scope](#7-out-of-scope)
8. [Requirement Traceability by Phase](#8-requirement-traceability-by-phase)
9. [Acceptance Criteria](#9-acceptance-criteria)
10. [Glossary](#10-glossary)
11. [Appendix A — Verified Competitive Landscape](#appendix-a--verified-competitive-landscape)

---

## 1. Introduction

### 1.1 Purpose

This document specifies the requirements for `d-cron`, an embeddable Go library
that provides safe, coordinated cron-style job scheduling across an arbitrary
number of replicas of a single application, using only a PostgreSQL database
that the application already operates.

It is intended for the project maintainers, prospective open-source
contributors, and internal reviewers at Mindfire.

### 1.2 Scope

`d-cron` is delivered as a Go module imported directly into an application
binary (`import "github.com/<org>/d-cron"`). It is **not** a standalone
server, daemon, or cluster. Jobs are ordinary Go functions registered at
startup; `d-cron`'s responsibility is deciding **which replica** runs a given
job at a given time, and reporting on what happened.

### 1.3 Problem Statement

In-process cron libraries such as `robfig/cron` maintain an in-memory timer
heap scoped to a single process. When an application is deployed at `N`
replicas — which is standard practice for availability, throughput,
zero-downtime rolling deploys, and autoscaling — each replica independently
reaches the trigger time and executes the job. A job scheduled for `0 2 * * *`
therefore executes `N` times.

Applications do not adopt multiple replicas in order to run scheduled jobs;
duplicate execution is an unintended consequence of a deployment decision made
for unrelated reasons. Existing mitigations each impose a material cost — a
dedicated single-replica scheduler deployment (single point of failure and a
second deployment to maintain), per-job lock racing (database load spike at
every trigger boundary), a new infrastructure dependency such as Redis or etcd,
or adoption of a full workflow engine.

### 1.4 Intended Audience and Reading Order

| Audience | Suggested sections |
| :--- | :--- |
| Reviewer / approver | 1, 2, 6, 9 |
| Contributor | 3, 4, 5, 8 |
| Evaluating user | 1.3, 2.2, 6, 7 |

### 1.5 References

- Companion design document: `SDS.md` (DCRON-SDS-001)
- PostgreSQL advisory lock functions —
  https://www.postgresql.org/docs/current/functions-admin.html
- M. Kleppmann, *How to do distributed locking* (fencing tokens)
- Verified competitor sources — see [Appendix A](#appendix-a--verified-competitive-landscape)

---

## 2. Overall Description

### 2.1 Product Perspective

`d-cron` occupies the gap between a single-process cron library and a
distributed workflow engine:

```
robfig/cron ──── d-cron ──── River / asynq ──── Dkron ──── Temporal
in-process      coordinated   task queue        standalone   workflow
cron only       cron          + cron            cluster      engine
                (this project)
```

### 2.2 Product Functions Summary

| # | Function |
| :-- | :--- |
| 1 | Elect at most one leader replica using a PostgreSQL advisory lock |
| 2 | Run the cron clock and fire registered jobs on the leader only |
| 3 | Detect leader loss and promote a standby automatically |
| 4 | Isolate job panics from the host application |
| 5 | Retry failed executions with configurable backoff |
| 6 | Expose leadership and execution state via metrics and an embedded UI |
| 7 | Issue monotonic fencing tokens so stale *same-database* writes are rejected |
| 8 | Apply configurable policy for missed runs, timeouts, and overlap |

### 2.3 User Classes

| Class | Description | Primary need |
| :--- | :--- | :--- |
| Application developer | Registers jobs in Go code | Simple API, no new infrastructure |
| Platform / SRE | Operates the deployment | Observability, safe failover, no SPOF |
| Contributor | Extends the library | Clear design, testable seams |

### 2.4 Operating Environment

- Go 1.22 or later
- PostgreSQL 12 or later
- Any deployment topology: bare process, Docker, Kubernetes, ECS, Nomad
- Any replica count including 1 (must degrade to plain in-process cron)

### 2.5 Design and Implementation Priorities

In descending order of precedence, used to resolve requirement conflicts:

1. **Safety** — never silently double-execute; never silently skip a run
2. **Zero new infrastructure** — no Redis, etcd, or additional daemon
3. **Adoption simplicity** — no mandatory migration for core scheduling
4. **Observability** — leadership and execution state always inspectable
5. **Feature breadth** — deliberately last

---

## 3. Functional Requirements

Requirement keywords follow RFC 2119 (**MUST**, **SHOULD**, **MAY**).
`P1`–`P4` denote the delivery phase (see [Section 8](#8-requirement-traceability-by-phase)).

### 3.1 Leader Election (FR-1xx)

| ID | Phase | Requirement |
| :--- | :-- | :--- |
| FR-101 | P1 | The system **MUST** elect at most one leader per `(database, namespace)` pair using a PostgreSQL session-level advisory lock. |
| FR-102 | P1 | Lock acquisition **MUST** be non-blocking (`pg_try_advisory_lock`); a replica that fails to acquire **MUST NOT** block application startup. |
| FR-103 | P1 | A replica that fails acquisition **MUST** become a standby and retry at a configurable interval (default 5s, jittered). |
| FR-104 | P1 | The leader **MUST** hold a dedicated database connection for the lifetime of its leadership, reserved exclusively for the lock. |
| FR-105 | P1 | Loss of the lock connection **MUST** cause the replica to relinquish leadership and revert to standby within one retry interval. |
| FR-106 | P1 | The system **MUST** support a configurable `namespace` so that multiple independent applications, or multiple environments, can share one database without contending for the same lock. |
| FR-107 | P1 | On `SIGTERM` or explicit `Stop()`, the leader **MUST** release the advisory lock explicitly via `pg_advisory_unlock` rather than relying on connection teardown, so that lock release does not depend on socket reaping behaviour. |
| FR-108 | P1 | The system **MUST NOT** rely on runtime probing to detect a transaction-pooling proxy. Because such detection is not reliably possible (see C-01), the system **MUST** instead require the operator to supply a connection the operator asserts is session-stable, **MUST** default to refusing to start unless that assertion is made explicitly in configuration, and **MUST** document the hazard. |
| FR-112 | P1 | The system **MUST** verify at startup that the caller's pool can supply a dedicated connection without exhausting it, and **MUST** refuse to start with an actionable error if it cannot. |
| FR-113 | P1 | The system **MUST** require, and verify where possible, that the lock connection has server- or DSN-level liveness detection enabled (`tcp_keepalives_*` and/or `client_connection_check_interval`), and **MUST** warn loudly when these are at PostgreSQL's disabled defaults, because without them a lock held by a dead or partitioned host is not released for hours (see C-06). |
| FR-114 | P1 | Liveness verification of the leader's own lock **MUST NOT** be implemented by re-invoking `pg_try_advisory_lock`, because advisory locks are re-entrant and repeated acquisition increments a counter that a single `pg_advisory_unlock` will not clear. |
| FR-109 | P2 | The system **MUST** expose the current leadership state (`leader` \| `standby` \| `unknown`) programmatically. |
| FR-110 | P3 | The system **MUST** maintain a strictly monotonic leader epoch counter that increments on every successful leadership acquisition. |
| FR-111 | P4 | The coordination mechanism **SHOULD** be abstracted behind an interface permitting alternative backends without changes to scheduling logic. |

### 3.2 Job Registration and Scheduling (FR-2xx)

| ID | Phase | Requirement |
| :--- | :-- | :--- |
| FR-201 | P1 | The system **MUST** accept job registration comprising a unique job name, a schedule expression, and a Go function of signature `func(context.Context) error`. |
| FR-202 | P1 | The system **MUST** support standard 5-field cron expressions. |
| FR-203 | P1 | The system **MUST** reject duplicate job names at registration time with a descriptive error. |
| FR-204 | P1 | The system **MUST** validate all schedule expressions at registration time, before the scheduler starts. |
| FR-205 | P1 | The system **MUST** support fixed-interval schedules (e.g. "every 30s") in addition to cron expressions. |
| FR-206 | P1 | The system **MUST** support a configurable IANA timezone per scheduler instance, defaulting to UTC. |
| FR-207 | P1 | Only the current leader **MUST** evaluate schedules and dispatch executions; standbys **MUST NOT** run a cron clock. |
| FR-208 | P1 | Fire-time decisions **MUST** be made solely from the clock of the replica currently holding the lock. The system **MUST NOT** compare timestamps across replicas for scheduling decisions. |
| FR-209 | P2 | The system **SHOULD** support one-off jobs scheduled for a specific future instant. |
| FR-210 | P3 | The system **SHOULD** support interval-since-last-success scheduling, where the next run is computed from the completion of the previous successful run. |
| FR-211 | P4 | The system **SHOULD** permit jobs to be added, paused, resumed, and removed at runtime without a restart. |
| FR-212 | P1 | The system **MUST** support seconds-precision cron expressions (6-field) as an opt-in. |

### 3.3 Execution Safety (FR-3xx)

| ID | Phase | Requirement |
| :--- | :-- | :--- |
| FR-301 | P1 | Every job invocation **MUST** be wrapped in a panic recovery boundary. A panic on the job's own goroutine **MUST NOT** terminate the host application process. A panic on a goroutine the job itself spawns cannot be recovered by the library; this limitation **MUST** be documented rather than implied away. |
| FR-302 | P1 | A recovered panic **MUST** be logged with the full stack trace and recorded as a failed execution. |
| FR-303 | P1 | Each job **MUST** receive a `context.Context` that is cancelled on scheduler shutdown. |
| FR-304 | P1 | Each job **MUST** support a configurable execution timeout, enforced via context cancellation, with a documented default. |
| FR-305 | P1 | A job execution exceeding its timeout **MUST NOT** prevent the scheduler from evaluating or dispatching subsequent runs of other jobs. |
| FR-306 | P1 | The system **MUST** support a configurable per-job retry policy with exponential backoff, a maximum attempt count, and optional jitter. |
| FR-307 | P1 | Retries **MUST** be abandoned if leadership is lost mid-retry-sequence. |
| FR-308 | P3 | The system **MUST** support a configurable overlap policy per job: `skip` (default), `queue`, or `allow`, governing behaviour when a previous run of the same job is still executing. |
| FR-309 | P3 | The system **MUST** inject the current leader epoch into each job's `context.Context`, retrievable via an exported accessor. |
| FR-310 | P3 | All `d-cron`-owned database writes describing an execution **MUST** be conditioned on the current leader epoch, and **MUST** be rejected if the epoch is stale. |
| FR-311 | P3 | The system **MUST** provide a helper enabling application code to guard its own writes with the same epoch, so that user job logic can be fenced. |
| FR-312 | P3 | The system **MUST** support a configurable missed-run policy per job: `skip` (default) or `catch_up`, governing behaviour when no leader was active at a scheduled trigger time. |
| FR-313 | P3 | Under `catch_up`, the system **MUST** enforce a configurable maximum lookback window and **MUST NOT** dispatch more than a configurable number of catch-up runs per job. |
| FR-314 | P1 | The system **MUST** provide a stable, deterministic idempotency key per execution, derived from job name and scheduled fire time, for use by application code. |
| FR-315 | P1 | Graceful shutdown **MUST** wait for in-flight jobs up to a configurable drain timeout before cancelling them. |

### 3.4 Observability (FR-4xx)

| ID | Phase | Requirement |
| :--- | :-- | :--- |
| FR-401 | P1 | The system **MUST** emit structured logs via `log/slog`, and **MUST NOT** impose a logging framework on the host application. |
| FR-402 | P1 | The system **MUST** log every leadership transition at `INFO`, including the acquiring instance identifier. |
| FR-403 | P2 | The system **MUST** expose Prometheus metrics covering, at minimum: leadership state gauge, leadership transition counter, per-job execution counter partitioned by outcome, per-job duration histogram, and per-job last-success timestamp gauge. |
| FR-404 | P2 | Metrics registration **MUST** accept a caller-supplied registry rather than mandating the default global registry. |
| FR-405 | P2 | The system **MUST** provide an embedded web UI as a standard `http.Handler`, mountable at any path on the application's existing router. |
| FR-406 | P2 | The UI **MUST** display: current leadership state and instance ID, all registered jobs with schedules, last run outcome and duration per job, next scheduled run per job, and recent execution history. |
| FR-407 | P2 | The UI **MUST** be servable without external network access; all assets **MUST** be embedded via `embed.FS`. |
| FR-408 | P2 | The UI **MUST** be disabled by default and require explicit opt-in, and the project **MUST** document that the host application is responsible for authenticating it. |
| FR-409 | P2 | The system **SHOULD** support a failure notification hook invoked on terminal job failure, with a webhook implementation provided. |
| FR-410 | P3 | The system **SHOULD** emit OpenTelemetry spans per execution when a tracer provider is supplied. |
| FR-411 | P2 | The system **MUST** expose a health/readiness check reporting coordination-backend reachability, suitable for use as a Kubernetes probe. |

### 3.5 Persistence (FR-5xx)

| ID | Phase | Requirement |
| :--- | :-- | :--- |
| FR-501 | P1 | Core scheduling and leader election **MUST** require zero database tables and zero migrations. |
| FR-502 | P2 | Execution history persistence **MUST** be opt-in. When enabled, the system **MUST** create and own its schema. |
| FR-503 | P2 | All `d-cron` tables **MUST** reside in a configurable schema, defaulting to a dedicated non-`public` schema. |
| FR-504 | P2 | Schema creation and migration **MUST** be idempotent and safe to execute concurrently from all replicas at startup. |
| FR-505 | P2 | Execution history **MUST** be subject to a configurable retention policy with automatic pruning performed by the leader. |
| FR-506 | P1 | The system **MUST** accept an existing `*sql.DB` or `pgxpool.Pool` and **MUST NOT** open connections outside the caller's configured pool, except where the operator supplies a separate dedicated lock DSN under FR-108. |
| FR-508 | P1 | The system **MUST** document that the reserved lock connection (FR-104) is drawn *from* the caller's pool and therefore reduces the pool's available capacity by one for the duration of leadership. |
| FR-507 | P3 | The leader epoch **MUST** be persisted such that monotonicity survives complete cluster restart. |

### 3.6 Configuration and API (FR-6xx)

| ID | Phase | Requirement |
| :--- | :--- | :--- |
| FR-601 | P1 | Configuration **MUST** use the functional-options pattern; the zero-configuration path **MUST** be usable with only a database handle. |
| FR-602 | P1 | The public API surface **MUST** be minimal and **MUST** follow semantic versioning once v1.0.0 is released. |
| FR-603 | P1 | The library **MUST NOT** call `os.Exit`, `panic` at package level, or write to `stdout` directly. |
| FR-604 | P1 | The library **MUST** be usable with a replica count of one, behaving as an ordinary in-process cron scheduler with no behavioural surprises. |
| FR-605 | P4 | The system **SHOULD** expose a REST or gRPC administrative API for runtime job management, gated behind explicit opt-in. |

---

## 4. Non-Functional Requirements

### 4.1 Performance (NFR-1xx)

| ID | Requirement |
| :--- | :--- |
| NFR-101 | Steady-state coordination overhead **MUST** be O(1) with respect to job count. The system **MUST NOT** perform per-job coordination round-trips at trigger time. |
| NFR-102 | Standby polling **MUST** generate no more than one database round-trip per replica per retry interval. |
| NFR-103 | Leader failover **MUST** complete within 2× the configured retry interval under graceful shutdown, and within 3× under **process** termination on a live host (where the kernel closes the socket). Under host death or network partition, failover latency is bounded only by the configured TCP keepalive / `client_connection_check_interval` settings, **not** by the retry interval — see C-06. The system **MUST NOT** state a retry-interval-based bound for those cases. |
| NFR-104 | Steady-state memory overhead **MUST** be under 10 MB for 1,000 registered jobs. |
| NFR-105 | The library **MUST** consume no more than one persistent connection *from* the caller's pool per replica, and **MUST NOT** open additional connections of its own unless a dedicated lock DSN is explicitly configured (FR-108). |
| NFR-106 | Scheduler tick evaluation **MUST NOT** block on job execution; dispatch **MUST** be asynchronous. |

### 4.2 Reliability (NFR-2xx)

| ID | Requirement |
| :--- | :--- |
| NFR-201 | The system **MUST** provide the correctness guarantees stated in [Section 6](#6-correctness-model) and **MUST NOT** claim stronger ones in code, documentation, or marketing material. |
| NFR-202 | Temporary database unavailability **MUST NOT** crash the host application; the scheduler **MUST** revert to standby and resume election attempts on recovery. |
| NFR-203 | No single replica **MAY** be structurally required for correct operation; there **MUST** be no designated scheduler node. |
| NFR-204 | All exported behaviour affecting correctness **MUST** be covered by tests executed against a real PostgreSQL instance. |

### 4.3 Usability (NFR-3xx)

| ID | Requirement |
| :--- | :--- |
| NFR-301 | Minimal working integration **MUST** be achievable in 10 lines of Go or fewer. |
| NFR-302 | Migration from `robfig/cron` **MUST** be documented as an explicit step-by-step guide. |
| NFR-303 | Every error returned **MUST** be actionable, naming the affected job or configuration key. |
| NFR-304 | The README **MUST** state, honestly and prominently, the correctness model and the known constraints of [Section 5](#5-constraints-and-assumptions). |

### 4.4 Maintainability and Portability (NFR-4xx)

| ID | Requirement |
| :--- | :--- |
| NFR-401 | The library **MUST** have no third-party dependencies in its core scheduling and election path beyond a PostgreSQL driver. |
| NFR-402 | Optional features (metrics, UI, tracing) **MUST** reside in subpackages so that unused dependencies are not linked into the host binary. |
| NFR-403 | The system **MUST** support both `database/sql` and `pgx` native pools. |
| NFR-404 | The system **MUST** operate identically on Linux, macOS, and Windows, and **MUST NOT** depend on container- or orchestrator-specific facilities. |
| NFR-405 | Public API documentation coverage **MUST** be complete for all exported identifiers. |

### 4.5 Security (NFR-5xx)

| ID | Requirement |
| :--- | :--- |
| NFR-501 | The system **MUST NOT** log connection strings, credentials, or job arguments at any level. |
| NFR-502 | The embedded UI **MUST** be read-only in P2. Any mutating control surface introduced later **MUST** require explicit opt-in and documented authentication. |
| NFR-503 | All database interaction **MUST** use parameterised queries. |
| NFR-504 | The project **MUST** publish a `SECURITY.md` and run dependency vulnerability scanning in CI. |

---

## 5. Constraints and Assumptions

### 5.1 Hard Constraints

| ID | Constraint |
| :--- | :--- |
| C-01 | **Connection pooler incompatibility, and it is not reliably detectable.** Session-level advisory locks are bound to a database session. A transaction-pooling proxy (e.g. PgBouncer in `transaction` mode, or comparable serverless poolers) may hand one server session to multiple clients, which breaks lock semantics in two observed ways: a lock acquired through the pooler remains held after the acquiring client disconnects (orphaned, unreleasable), and a second client can acquire the *same* key successfully because it is handed the same server session and advisory locks are re-entrant — producing two simultaneous leaders. Empirically verified against PgBouncer 1.22 in `transaction` mode. Critically, **runtime detection is not dependable**: comparing `pg_backend_pid()` across round-trips returns identical PIDs when the server pool is uncontended, which is precisely the condition at application startup. `SHOW pool_mode` is forwarded to the server and errors identically to direct PostgreSQL, yielding no signal. `d-cron` therefore requires operator assertion of session stability, or a dedicated direct lock DSN, rather than promising automatic detection (FR-108). |
| C-06 | **Advisory locks are not released on host death or partition.** PostgreSQL releases a session advisory lock when the backend *process* exits. If the leader's host dies, freezes, or is partitioned, the backend blocks in `recv()` and never learns. PostgreSQL's defaults are `tcp_keepalives_idle = 0`, `tcp_keepalives_interval = 0`, `tcp_keepalives_count = 0` (deferring to the OS, ≈2h11m on Linux) and `client_connection_check_interval = 0` (disabled) — so the lock can remain held for hours and **no replica can be promoted**. Operators **MUST** configure keepalives or `client_connection_check_interval` for the failover guarantees to hold (FR-113). |
| C-07 | **Advisory locks are re-entrant.** Two successful `pg_try_advisory_lock` calls on one session both return `true` and increment a counter; a single `pg_advisory_unlock` then leaves the lock held. Any liveness check must therefore never re-acquire (FR-114). |
| C-02 | **PostgreSQL is mandatory in P1–P3.** The project is deliberately not database-agnostic at launch. |
| C-03 | **Jobs execute in-process.** `d-cron` cannot schedule work for non-Go services or external binaries. |
| C-04 | **Advisory locks are per-database, not per-cluster.** Replicas must share one logical database to be coordinated. |
| C-05 | **Read replicas cannot participate.** The lock connection requires the primary. |

### 5.2 Assumptions

| ID | Assumption |
| :--- | :--- |
| A-01 | The application already operates a PostgreSQL database it can reach at startup. |
| A-02 | Replicas run the same binary and register an identical job set. Divergent registration during rolling deploys is tolerated but not coordinated. |
| A-03 | Job functions are, or can be made, idempotent. `d-cron` reduces duplicate execution but cannot eliminate it (see [Section 6](#6-correctness-model)). |
| A-04 | Host clocks are NTP-synchronised to within a few seconds. `d-cron` never compares clocks across replicas (FR-208), so skew degrades fire-time accuracy but not correctness. |
| A-05 | The application controls its own HTTP router and can mount a handler (required for FR-405). |

---

## 6. Correctness Model

This section is normative and constrains all project communication.

### 6.1 What is guaranteed

- **Under normal operation:** at most one execution per scheduled fire time.
- **Under failure, with `MissedCatchUp` configured and history enabled:** at
  least one execution per scheduled fire time within the configured lookback
  window (FR-312, FR-313).
- **Under failure, with the default `MissedSkip` policy:** a fire time at which
  no leader was active produces **zero** executions. This is the default, and
  the documentation **MUST** state it plainly. The project **MUST NOT** claim an
  unqualified "at least once" guarantee.
- **Under leadership change:** any execution originating from a demoted leader
  is detectably stale via its fencing epoch and will be rejected by
  `d-cron`-owned writes in the same database (FR-310). Effects outside that
  database cannot be fenced (see §6.2).

Note also that missed-run *counting* (`dcron_missed_runs_total`) is a Phase 3
capability dependent on the Phase 2 store. In Phase 1 and Phase 2, skipped fire
times are logged but not durably counted, so §2.5's "never silently skip a run"
priority is only fully realised from Phase 3 onward. This gap **MUST** be stated
in the Phase 1 README.

### 6.2 What is not guaranteed

**`d-cron` does not provide exactly-once execution.** Exactly-once execution of
side-effecting work is not achievable in a distributed system without
cooperation from the downstream effect. The project **MUST NOT** claim it.

The residual duplicate-execution window arises as follows: the leader's process
stalls (stop-the-world GC pause, host suspension, or kernel scheduling delay)
long enough for its lock connection to drop; a standby acquires the lock and
becomes leader at epoch `n+1`; the original leader resumes still believing
itself leader at epoch `n` and may begin or continue a job invocation. The
inverse also holds: a transient network fault may release the lock while the
leader is alive and mid-execution.

`d-cron`'s mitigations are epoch fencing (FR-309–FR-311), which prevents stale
work from committing, and deterministic idempotency keys (FR-314), which allow
application code to deduplicate. Responsibility for idempotency of the job's
own side effects remains with the application author, and the documentation
**MUST** say so plainly (NFR-304).

### 6.3 Comparison of failure modes

| Scenario | Behaviour |
| :--- | :--- |
| Leader process exits cleanly | Lock explicitly released; standby promoted within one retry interval |
| Leader process killed (`SIGKILL`), host alive | Kernel closes socket; PostgreSQL reaps backend and releases lock; standby promoted |
| **Leader host dies or is partitioned** | Backend blocks in `recv()`; **lock is NOT released until keepalives or `client_connection_check_interval` expire**. With PostgreSQL defaults this is hours, during which **no replica is promoted and no jobs run**. Requires operator configuration — see C-06 |
| Leader stalls, connection survives | Leader retains lock; scheduled runs are late, not duplicated |
| Leader stalls, connection drops | Split-brain window; epoch fencing rejects stale same-database writes |
| Database unreachable from all replicas | No leader; no executions; resumes on recovery per missed-run policy |
| Behind a transaction-pooling proxy | Two clients may hold the same lock key; orphaned locks persist after disconnect. Mitigated only by operator assertion / dedicated DSN (C-01, FR-108) |

---

## 7. Out of Scope

The following are explicitly excluded to preserve the product's positioning.
Requests for them should be answered by pointing to the named alternative.

| Excluded | Rationale / alternative |
| :--- | :--- |
| Workflow orchestration, DAGs, job chaining, saga compensation | Use Temporal or Cadence |
| General-purpose task queue with producers, consumers, and payload serialisation | Use River or asynq |
| Execution of shell commands, containers, or HTTP endpoints as jobs | Use Dkron |
| Standalone server or cluster daemon | Contradicts the in-process premise |
| Non-Go job execution | Out of scope for all phases |
| Sub-second scheduling precision | Not a target |
| Multi-region or cross-cluster coordination | Out of scope; single logical database only |
| Built-in authentication for the embedded UI | Delegated to the host application (FR-408) |

---

## 8. Requirement Traceability by Phase

### Phase 1 — MVP: "coordinated cron"

Deliverable: a Go library that replaces `robfig/cron` in a multi-replica
deployment with no migration and no new infrastructure.

FR-101–FR-108, FR-112, FR-113, FR-114, FR-201–FR-208, FR-212,
FR-301–FR-307, FR-314, FR-315, FR-401, FR-402, FR-501, FR-506, FR-508,
FR-601–FR-604
NFR-101, NFR-102, NFR-103, NFR-104, NFR-105, NFR-106, NFR-201–NFR-204,
NFR-301–NFR-303, NFR-401, NFR-403, NFR-404, NFR-501, NFR-503

### Phase 2 — Observability

Deliverable: Prometheus metrics, embedded read-only dashboard, opt-in
execution history, health probe.

FR-109, FR-209, FR-403–FR-409, FR-411, FR-502–FR-505
NFR-304, NFR-402, NFR-405, NFR-502, NFR-504

### Phase 3 — Correctness hardening

Deliverable: epoch fencing end to end, missed-run catch-up, overlap policy,
interval-since-success scheduling, optional tracing.

FR-110, FR-210, FR-308–FR-313, FR-410, FR-507

### Phase 4 — Extensibility

Deliverable: pluggable coordination backend, runtime job management,
administrative API.

FR-111, FR-211, FR-605

---

## 9. Acceptance Criteria

Phase 1 is accepted when all of the following hold.

| ID | Criterion |
| :--- | :--- |
| AC-01 | With 5 replicas and a job scheduled every minute, exactly one execution is observed per minute over a 30-minute run. |
| AC-02 | Killing the leader with `SIGKILL` **on a live host** results in a new leader and a resumed schedule within 3× the retry interval, with no duplicate execution of the in-flight fire time. |
| AC-02b | With keepalives configured per C-06, severing the leader's host at the network layer results in promotion within the configured keepalive window. With keepalives at PostgreSQL defaults, the test **MUST** demonstrate that promotion does *not* occur promptly — this is the documented limitation, and the test exists to prevent the guarantee being overstated. |
| AC-03 | Rolling-restarting all replicas one at a time produces no duplicated and no skipped executions. |
| AC-04 | A job that panics is recovered, logged with stack trace, recorded as failed, and does not terminate the process. |
| AC-05 | A job exceeding its timeout is cancelled and does not delay other jobs' dispatch. |
| AC-06 | With 200 registered jobs across 5 replicas, database query volume attributable to `d-cron` remains O(1) per retry interval and shows no spike at minute boundaries. |
| AC-07 | Starting without an explicit operator assertion of session stability (or a dedicated lock DSN) fails at startup with an explicit, actionable message naming the pooler hazard (FR-108). A test **MUST** also document the two failure behaviours observed through PgBouncer `transaction` mode — orphaned locks and duplicate acquisition — so no future contributor reintroduces a probe-based detection claim. |
| AC-08 | A single-replica deployment behaves identically to `robfig/cron` for all registered jobs. |
| AC-09 | Zero database tables exist in any schema after a full Phase 1 lifecycle. |
| AC-10 | Test suite passes against PostgreSQL 12 and the current stable release. |

---

## 10. Glossary

| Term | Definition |
| :--- | :--- |
| **Replica** | One running instance of the application binary. |
| **Leader** | The single replica currently holding the advisory lock and running the cron clock. |
| **Standby** | A replica that is not leader; it polls for the lock and executes no scheduled jobs. |
| **Advisory lock** | A PostgreSQL application-defined lock identified by an integer key, held for the duration of a session, requiring no table. |
| **Leader epoch** | A strictly increasing integer incremented on each leadership acquisition, used as a fencing token. |
| **Fencing token** | A monotonic value carried with work, allowing a resource to reject writes from a superseded actor. |
| **Split-brain** | A state in which two replicas simultaneously believe they hold leadership. |
| **Thundering herd** | A load spike caused by all replicas contending for the same resource at the same instant. |
| **Per-job lock racing** | A coordination strategy in which every replica wakes at every trigger time and competes for a per-execution lock. |
| **Consistent hash ring** | A coordination strategy that deterministically partitions jobs across nodes by hashing. |
| **Missed run** | A scheduled fire time at which no leader was active. |
| **Overlap** | A fire time reached while a previous run of the same job is still executing. |
| **Namespace** | A logical partition of the lock keyspace allowing multiple applications or environments to share one database. |
| **Idempotency key** | A deterministic identifier for a logical execution, enabling downstream deduplication. |

---

## Appendix A — Verified Competitive Landscape

Verified 2026-08-13 against primary sources. This appendix exists because an
earlier internal draft contained material errors; the corrected findings are
recorded here to prevent their reintroduction.

| Project | Coordination mechanism | Infrastructure | Scale | Notes |
| :--- | :--- | :--- | :--- | :--- |
| `robfig/cron` | None | None | — | In-memory timer heap, single process |
| `go-co-op/gocron` v2 | **`DistributedElector` (leader election) and `DistributedLocker` (per-job)** | etcd (elector); Redis or GORM/SQL (locker) | 7.1k ★, v2.21.2 (2026-05-12) | **Closest competitor.** Has leader election, contrary to an earlier internal draft. Only elector implementation is `gocron-etcd-elector`. `gocron-gorm-lock` is Postgres-capable but is per-job locking and requires a `cron_job_locks` table (18 ★). `gocron-ui` exists. |
| `libi/dcron` | **Consistent hash ring** — maintainers explicitly reject distributed locking on clock-skew grounds | Redis or etcd (via `dcron-contrib` since v0.6.0) | 464 ★ | **Not per-job locking**, contrary to an earlier internal draft. No PostgreSQL support. |
| `hibiken/asynq` | **None for the scheduler.** Documentation states: "You have to ensure only a single scheduler is running for a schedule at a time, otherwise you'd end up with duplicate tasks." | Redis | 13.3k ★, v0.26.0 (2026-02-03) | Task queue first. Official guidance is a single dedicated scheduler instance — a single point of failure. |
| `riverqueue/river` | Leader election via unlogged `river_leader` table, 5s TTL, `LISTEN`/`NOTIFY` on resign; leadership per database+schema | PostgreSQL + queue tables and migrations | 5.2k ★ | Periodic jobs are free in OSS but held in memory and may skip runs across leader elections. **Durable Periodic Jobs** (persisted run times) is a Pro feature, added River Pro v0.15. |
| `Dkron` | Raft consensus + Serf gossip | Standalone server cluster | 4.7k ★ | Executes external commands, containers, HTTP. Dkron Pro is **$450/year**. |
| Quartz | JDBC row-level locking | RDBMS | — | Java only. |
| Kubernetes `CronJob` | Control-plane owned | Kubernetes | — | Couples scheduling to the orchestrator; no in-process Go execution. |

### A.1 Defensible differentiation

The wedge is the combination, not any single attribute:

1. **Leader election rather than per-job lock racing** — removes the
   trigger-boundary thundering herd (NFR-101).
2. **PostgreSQL-only, no Redis or etcd** — the only elector in the Go ecosystem
   is etcd-backed; the sole PostgreSQL-capable option is a 18-star per-job
   locker requiring a table.
3. **Monotonic epoch fencing tokens** — not present in any surveyed competitor.
4. **Zero migrations for core scheduling** (FR-501) — scoped honestly: history
   and fencing add an opt-in schema.

### A.2 Claims that must not be made

- Not "exactly-once" (see [Section 6.2](#62-what-is-not-guaranteed)).
- Not "table-less" without qualification — true for P1 election only.
- Not "the only Go library with distributed cron leader election" —
  `gocron` v2 has one.
- Not "gocron requires Redis" — `gocron-gorm-lock` is SQL-backed.
- Not "having a dashboard is a differentiator" — `gocron-ui` exists.
- Not "advisory locks give us failure detection for free" — true only for
  process death on a live host; host death and partition need keepalive
  configuration (C-06).
- Not "we detect connection poolers automatically" — not reliably possible
  (C-01).

---

*End of document.*
