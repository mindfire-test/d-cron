# `d-cron` — GitHub Issue Backlog

Derived from `SRS.md` (DCRON-SRS-001) and `SDS.md` (DCRON-SDS-001), 2026-08-13.
Covers all four phases. **52 issues.**

Every issue is written to be copy-pasted directly into GitHub: title, labels,
milestone, dependencies, requirement traceability, and acceptance criteria.
Issue numbers below (`#1`–`#51`) are *backlog* numbers used for the dependency
graph — renumber to real GitHub issue numbers once created, or use the
`gh` bulk-create script in [Appendix C](#appendix-c--bulk-create-with-gh-cli),
which preserves ordering so backlog numbers match issue numbers on a fresh repo.

---

## Contents

- [Setup: labels and milestones](#setup-labels-and-milestones)
- [Phase 0 — Bootstrap](#phase-0--bootstrap-5-issues) (5)
- [Phase 1 — MVP](#phase-1--mvp-26-issues) (26)
- [Phase 2 — Observability](#phase-2--observability-8-issues) (8)
- [Phase 3 — Correctness hardening](#phase-3--correctness-hardening-8-issues) (8)
- [Phase 4 — Extensibility](#phase-4--extensibility-4-issues) (4)
- [Appendix A — Dependency graph](#appendix-a--dependency-graph)
- [Appendix B — Requirement coverage matrix](#appendix-b--requirement-coverage-matrix)
- [Appendix C — Bulk create with `gh` CLI](#appendix-c--bulk-create-with-gh-cli)

---

## Setup: labels and milestones

Create these before importing issues.

### Labels

| Label | Colour | Meaning |
| :--- | :--- | :--- |
| `area/elector` | `#1D76DB` | Leader election, advisory locks, connection handling |
| `area/clock` | `#0E8A16` | Schedules, cron parsing, the fire loop |
| `area/executor` | `#5319E7` | Job invocation, panic, timeout, retry |
| `area/store` | `#B60205` | Persistence, migrations, history |
| `area/api` | `#FBCA04` | Public surface, options, errors |
| `area/observability` | `#006B75` | Logs, metrics, UI, tracing |
| `area/testing` | `#BFD4F2` | Test harness, integration, soak |
| `area/docs` | `#C5DEF5` | README, guides, examples |
| `phase/0` … `phase/4` | `#EEEEEE` | Roadmap phase |
| `type/feature` | `#A2EEEF` | New capability |
| `type/bug` | `#D73A4A` | Defect |
| `type/chore` | `#FEF2C0` | Tooling, CI, housekeeping |
| `type/spike` | `#D4C5F9` | Time-boxed investigation |
| `priority/P0` | `#B60205` | Blocks the phase |
| `priority/P1` | `#D93F0B` | Required for the phase |
| `priority/P2` | `#FBCA04` | Desirable |
| `safety-critical` | `#000000` | **Correctness of distributed guarantees depends on this.** Requires two reviewers |
| `good-first-issue` | `#7057FF` | Self-contained, low context needed |
| `help-wanted` | `#008672` | Open to outside contributors |

> **On `safety-critical`:** eleven issues carry it. These are the ones where a
> plausible-looking implementation silently breaks the product's core promise.
> Enforce two approvals and a linked test.

### Milestones

| Milestone | Definition of done |
| :--- | :--- |
| `Phase 0 — Bootstrap` | Repo builds, CI green, name settled |
| `Phase 1 — MVP` | All of SRS AC-01…AC-10 pass; publishable as v0.1.0 |
| `Phase 2 — Observability` | Metrics, UI, history, health probe |
| `Phase 3 — Correctness hardening` | Epoch fencing end-to-end, policies |
| `Phase 4 — Extensibility` | Pluggable backend, runtime job management |

---

## Phase 0 — Bootstrap (5 issues)

---

### #1 — Decide the project name (blocks everything public)

**Labels:** `type/spike` `phase/0` `priority/P0`
**Milestone:** Phase 0 — Bootstrap
**Blocked by:** —
**Traces:** SDS O-8

`d-cron` collides directly with [`libi/dcron`](https://github.com/libi/dcron),
an existing Go library with 464 stars solving an adjacent problem with a
different mechanism (consistent hashing). Shipping under a near-identical name
harms discoverability, invites confusion in issue trackers and search, and reads
as careless to anyone who knows the ecosystem.

This must be settled before the first public commit — renaming a Go module after
publication means an import-path break for every early adopter.

**Acceptance criteria**

- [ ] Candidate names checked against pkg.go.dev, GitHub search, and the Go
      module proxy for collisions
- [ ] Preferred name has an available GitHub org/repo and module path
- [ ] Decision recorded in `docs/adr/0001-name.md`
- [ ] SRS and SDS updated with the chosen name

**Notes**

Keep the "Postgres-native" and "leader election" angles in mind — a name hinting
at either is more discoverable than a generic cron pun.

---

### #2 — Repository scaffolding and licence

**Labels:** `type/chore` `phase/0` `priority/P0`
**Milestone:** Phase 0 — Bootstrap
**Blocked by:** #1
**Traces:** SDS §9, SDS O-7

Create the module and directory skeleton per SDS §9.

**Acceptance criteria**

- [ ] `go.mod` targeting Go 1.22, module path per #1
- [ ] Package skeleton: `internal/{elector,clock,executor,store}`,
      `metrics/`, `ui/`, `otel/`, `examples/`
- [ ] `LICENSE` — Apache-2.0 (chosen for the patent grant, SDS O-7)
- [ ] `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `CODEOWNERS`
- [ ] `.gitignore`, issue and PR templates
- [ ] `docs/adr/` with ADR template

**Notes**

`metrics`, `ui`, and `otel` are **separate packages on purpose** (SRS NFR-402) so
that apps not using them don't link `prometheus/client_golang` or the OTel SDK.
Do not put a Prometheus counter in the core executor — that mistake is easy to
make and hard to undo.

---

### #3 — CI: build, lint, race, multi-version PostgreSQL matrix

**Labels:** `type/chore` `area/testing` `phase/0` `priority/P0`
**Milestone:** Phase 0 — Bootstrap
**Blocked by:** #2
**Traces:** NFR-204, NFR-404, AC-10

**Acceptance criteria**

- [ ] GitHub Actions workflow: build, `go vet`, `golangci-lint`
- [ ] All tests run under `-race`
- [ ] Integration matrix against PostgreSQL **12** and current stable (AC-10)
- [ ] Cross-compile check for linux/darwin/windows (NFR-404)
- [ ] `govulncheck` on a schedule (NFR-504)
- [ ] Coverage reported, no hard gate initially

---

### #4 — Test harness: ephemeral PostgreSQL via testcontainers

**Labels:** `type/chore` `area/testing` `phase/0` `priority/P0`
**Milestone:** Phase 0 — Bootstrap
**Blocked by:** #2
**Traces:** NFR-204

Shared helper for spinning a real PostgreSQL per test package. Everything in the
elector and store areas depends on this, so land it early.

**Acceptance criteria**

- [ ] `internal/testutil` exposing `NewPostgres(t)` returning a ready `*sql.DB`
- [ ] Version selectable via env var so the CI matrix can drive it
- [ ] Helper to open a **second independent connection** (needed for
      concurrent-leader tests)
- [ ] Helper to forcibly terminate a backend (`pg_terminate_backend`) to
      simulate connection loss
- [ ] Skips cleanly with a clear message when Docker is unavailable

---

### #5 — Test harness: PgBouncer in `transaction` mode

**Labels:** `type/chore` `area/testing` `phase/0` `priority/P1` `safety-critical`
**Milestone:** Phase 0 — Bootstrap
**Blocked by:** #4
**Traces:** C-01, FR-108, AC-07

A real PgBouncer fronting the test PostgreSQL, in `transaction` pooling mode.
Required by #12 and #40.

**Acceptance criteria**

- [ ] Helper returns a `*sql.DB` routed through PgBouncer in `transaction` mode
- [ ] Pool size configurable so contention can be induced deliberately
- [ ] Documented in `CONTRIBUTING.md` as required for the safety test suite

**Notes**

This harness exists because the hazards were empirically confirmed during design
review against PgBouncer 1.22: a lock acquired through the pooler stayed held
after its client disconnected, and a second client acquired the *same* key
successfully. See SDS §3.4.

---

## Phase 1 — MVP (26 issues)

### Elector

---

### #6 — Lock key derivation and namespacing

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P0`
**Milestone:** Phase 1 — MVP
**Blocked by:** #2
**Traces:** FR-106

Advisory lock keys live in a single `bigint` keyspace global to the database.
Collisions are silent and miserable to debug, so derivation must be
deterministic and documented.

```go
// key = int64 from the first 8 bytes of sha256("d-cron:v1:" + namespace)
func lockKey(namespace string) int64
```

**Acceptance criteria**

- [ ] Deterministic derivation, `namespace` defaulting to `"default"`
- [ ] Resolved key exposed programmatically (the UI and logs need it)
- [ ] Logged at startup at `INFO`
- [ ] Table-driven tests including empty and unicode namespaces
- [ ] Doc comment explaining that two apps sharing a database **must** use
      distinct namespaces

**Notes**

Namespace collision is the single easiest way to misconfigure this library — two
apps sharing a namespace fight over one lock and one of them never schedules
anything (SDS §12 row 10). Docs must lead with it.

---

### #7 — Elector: acquire and hold the lock on a dedicated connection

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #4, #6
**Traces:** FR-101, FR-102, FR-104, NFR-105, FR-508

Session-level advisory locks are bound to one session, so the lock cannot be
taken on a pooled query — the pool may hand back a different connection next
time. The elector must call `db.Conn(ctx)` and hold that `*sql.Conn` for the
entire duration of leadership.

**Acceptance criteria**

- [ ] `pg_try_advisory_lock` (non-blocking, never `pg_advisory_lock`)
- [ ] Reserved `*sql.Conn` held for the leadership term
- [ ] Failed acquisition does **not** block application startup (FR-102)
- [ ] Doc comment states the reserved connection comes *from* the caller's pool
      and reduces its capacity by one (FR-508)
- [ ] Test: N electors against one database, exactly one acquires

**Notes**

Use `pg_try_` not `pg_advisory_lock` — a blocking acquire in a standby ties up a
connection indefinitely and makes graceful failover impossible to reason about.

---

### #8 — Elector state machine

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #7
**Traces:** FR-103, FR-105, FR-109, NFR-202

States: `UNKNOWN` → `LEADER` | `STANDBY`, with `DEMOTING` on the way back down
(SDS §3.5). `UNKNOWN` is a real state — before the first attempt, and after a
failed liveness probe — not a placeholder.

**Acceptance criteria**

- [ ] Four states implemented with explicit transitions
- [ ] Transitions emitted on a channel for the scheduler to consume
- [ ] Loss of the lock connection demotes within one retry interval (FR-105)
- [ ] Demotion drains before releasing (ordering matters — see #22)
- [ ] **Database unavailability never crashes the host application** — the
      scheduler reverts to standby and resumes election attempts on recovery
      (NFR-202)
- [ ] Test: database stopped and restarted mid-run; host process survives and
      a leader re-emerges
- [ ] Tests cover every transition edge including `LEADER → DEMOTING → STANDBY`

---

### #9 — Standby polling with jitter

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P1` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #8
**Traces:** FR-103, NFR-102, NFR-103

Standbys retry acquisition every 5s ±20% jitter.

**Acceptance criteria**

- [ ] Default 5s, configurable, ±20% jitter applied
- [ ] **Polls on a dedicated `db.Conn()`; closes it immediately on failure,
      retains it on success**
- [ ] One database round-trip per replica per interval, no more (NFR-102)
- [ ] Test: 10 standbys show no synchronised poll bursts

**Notes**

Two traps here, both of which an early SDS draft got wrong.

Jitter is not cosmetic: without it, all N standbys wake in lockstep and
reintroduce a thundering herd at exactly the moment a leader dies — the worst
possible time.

And standbys must **not** poll on a pooled connection. If
`pg_try_advisory_lock` succeeds on a pooled connection, the lock lands on a
session that is then returned to the pool: leadership is held on a connection
nobody controls and cannot reliably release.

---

### #10 — Liveness check that never re-acquires the lock

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #8
**Traces:** FR-114, C-07, SDS O-2

The leader must verify each interval that it still holds the lock. Holding it is
not the same as knowing you hold it.

**Acceptance criteria**

- [ ] Liveness = `SELECT 1` **plus** a `pg_locks` lookup filtered by our
      `objid` and `pg_backend_pid()`
- [ ] **Never calls `pg_try_advisory_lock` as part of the check**
- [ ] Failure transitions to `DEMOTING`
- [ ] Regression test: two `pg_try_advisory_lock(k)` on one session both return
      `true`, and a single `pg_advisory_unlock(k)` leaves it **still held**

**Notes**

⚠️ **Advisory locks are re-entrant.** Implementing the liveness check as a
re-`try_lock` is the natural, tempting reading — and it silently breaks
shutdown: the counter climbs every 5s and the single `pg_advisory_unlock` in #11
never releases, degrading failover to the connection-teardown path this whole
design exists to avoid. Verified against PostgreSQL 16. The regression test is
mandatory precisely so nobody reintroduces this.

---

### #11 — Explicit unlock on graceful shutdown

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P1`
**Milestone:** Phase 1 — MVP
**Blocked by:** #8
**Traces:** FR-107

**Acceptance criteria**

- [ ] `pg_advisory_unlock(key)` called explicitly before closing the connection
- [ ] Unlock happens **before** job drain begins, not after
- [ ] Return value checked and logged — `false` means we didn't hold it
- [ ] Works when `Stop()` is called without process exit
- [ ] Test: standby promotes within one poll interval after graceful stop

**Notes**

Don't repeat the wrong rationale from the SDS's first draft. Explicit unlock is
*not* needed because socket teardown is slow — on process exit the kernel sends
FIN immediately and the backend exits in milliseconds. The real reasons: it
releases the lock before draining (measurably shortening the gap), it works when
`Stop()` is called without exiting, and it doesn't depend on driver/OS behaviour
we don't control.

---

### #12 — Session-stability gate (pooler safety)

**Labels:** `area/elector` `area/api` `phase/1` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #5, #7
**Traces:** FR-108, C-01, AC-07

`d-cron` must refuse to start unless the operator has asserted session
stability or supplied a dedicated direct connection.

```go
dcron.WithSessionStableConnection()      // operator asserts direct / session-mode
dcron.WithDedicatedLockDSN(dsn string)   // d-cron opens its own direct conn
```

**Acceptance criteria**

- [ ] Startup fails without one of the two options
- [ ] Error names the hazard, describes both observed failure modes, and lists
      remedies (dedicated DSN, `session` pooling mode, or wait for Phase 4)
- [ ] `WithDedicatedLockDSN` opens and owns exactly one direct connection
- [ ] Regression test through PgBouncer proving the `pg_backend_pid()` probe
      does **not** distinguish transaction pooling

**Notes**

⚠️ **Do not implement runtime probe detection.** An earlier design proposed
comparing `pg_backend_pid()` across round-trips. Measured against PgBouncer
1.22, this returns the **same** PID every time — the pooler only reassigns
server sessions when the pool is contended, and at startup it isn't. A reliable
false negative in exactly the dangerous case. `SHOW pool_mode` is equally
useless: PgBouncer forwards it to the server, which errors identically to direct
PostgreSQL.

A detection scheme that silently passes in the dangerous case is worse than no
detection, because it converts an operator decision into a false assurance. The
regression test exists to stop a future contributor "improving" this back.

---

### #13 — Startup preflight: pool capacity

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P1`
**Milestone:** Phase 1 — MVP
**Blocked by:** #7
**Traces:** FR-112

**Acceptance criteria**

- [ ] Inspect `db.Stats().MaxOpenConnections` at startup
- [ ] Refuse to start when the pool cannot spare a connection
      (`MaxOpenConns(1)` deadlocks the app)
- [ ] Error names the setting and the required minimum
- [ ] Test with `MaxOpenConns(1)` and `(2)`

---

### #14 — Startup preflight: TCP keepalive detection and warning

**Labels:** `area/elector` `phase/1` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #7
**Traces:** FR-113, C-06

**Acceptance criteria**

- [ ] Read `tcp_keepalives_idle` and `client_connection_check_interval` at
      startup via `current_setting`
- [ ] Loud `WARN` when both are `0`, stating that a dead or partitioned leader
      will hold the lock for hours and **no replica will be promoted**
- [ ] Where the driver allows, set keepalives on our own lock connection's DSN
- [ ] Required settings documented in the quickstart, not buried

**Notes**

⚠️ This is the weakest link in the whole design and it must not be soft-pedalled.
PostgreSQL releases a session advisory lock when the *backend process* exits. If
the leader's host dies or is partitioned, the backend blocks in `recv()` and
never learns. Verified PostgreSQL 16 defaults:

```
tcp_keepalives_idle              = 0   -- defer to OS (~2h11m on Linux)
tcp_keepalives_interval          = 0
tcp_keepalives_count             = 0
client_connection_check_interval = 0   -- disabled
```

With those defaults the lock stays held for hours and **nothing runs at all** —
a worse outage than the duplicate execution we exist to prevent.

---

### Clock

---

### #15 — Cron expression parser

**Labels:** `area/clock` `phase/1` `type/feature` `priority/P0` `good-first-issue`
**Milestone:** Phase 1 — MVP
**Blocked by:** #2
**Traces:** FR-202, FR-204, FR-212, NFR-401, SDS O-1

Write our own rather than vendoring, to keep the core dependency-free
(NFR-401). Roughly 200 lines against a well-documented spec.

**Acceptance criteria**

- [ ] 5-field cron; 6-field with seconds behind `WithSecondsField()` (FR-212)
- [ ] Descriptors: `@yearly` `@monthly` `@weekly` `@daily` `@hourly`
- [ ] `@every <duration>`
- [ ] Ranges, steps, lists, `*`, and day/month names
- [ ] Validation at **registration** time with descriptive errors (FR-204)
- [ ] Table-driven tests including DST spring-forward and fall-back, leap years,
      and Feb 29
- [ ] Fuzz target for the parser

**Notes**

Well-scoped and self-contained — a good first contribution. Revisit the
build-vs-vendor decision (SDS O-1) if DST handling turns out hairier than
expected.

---

### #16 — `Schedule` interface, cron and interval implementations

**Labels:** `area/clock` `phase/1` `type/feature` `priority/P0`
**Milestone:** Phase 1 — MVP
**Blocked by:** #15
**Traces:** FR-202, FR-205

```go
type Schedule interface { Next(time.Time) time.Time }
```

**Acceptance criteria**

- [ ] `cronSchedule` (FR-202, FR-212)
- [ ] `intervalSchedule` for `@every 30s` (FR-205)
- [ ] Zero time returned signals "never again" (needed by `onceSchedule`, #33)
- [ ] Interface documented as the extension point for #33 and #45

---

### #17 — Scheduler loop: min-heap, async dispatch, timezone

**Labels:** `area/clock` `phase/1` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #16, #8
**Traces:** FR-206, FR-207, FR-208, NFR-106, FR-305

**Acceptance criteria**

- [ ] Min-heap of next-fire-times, same shape as `robfig/cron`
- [ ] **Only the leader runs a clock**; standbys run none (FR-207)
- [ ] Dispatch is asynchronous — the loop never blocks on job execution
      (NFR-106, FR-305)
- [ ] One `*time.Location` per scheduler, resolved once, default UTC (FR-206)
- [ ] On promotion, `next` computed forward from `time.Now()` — no attempt to
      reconstruct history
- [ ] Clock stops immediately on demotion
- [ ] Test: a job sleeping 10s does not delay other jobs' dispatch

**Notes**

**Fire times come only from the leader's own clock** (FR-208). Never read
`now()` from PostgreSQL for scheduling and never compare timestamps across
replicas. This is our answer to `libi/dcron`'s clock-skew objection to
lock-based coordination: skew between replicas cannot cause a double fire
because only one replica's clock is ever consulted. Skew makes jobs fire a few
seconds early or late relative to wall-clock truth, and we say so honestly.

Per-job timezones are deliberately **not** supported — mixed-timezone DST
transitions produce genuinely confusing skipped and doubled fire times.

---

### Executor

---

### #18 — Panic recovery boundary

**Labels:** `area/executor` `phase/1` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #2
**Traces:** FR-301, FR-302, AC-04

**Acceptance criteria**

- [ ] `recover()` in a deferred func inside the invocation wrapper
- [ ] `debug.Stack()` captured **inside** the deferred func
- [ ] Panic converted to a typed `*PanicError` carrying value and stack
- [ ] Logged with full stack, recorded as a failed execution
- [ ] Doc comment states the limitation: a panic on a goroutine the *job* spawns
      cannot be recovered and will terminate the process
- [ ] Test: panicking job does not kill the test process; stack contains the
      panicking frame

**Notes**

The stack must be captured inside the deferred func, before the stack unwinds
further — verified: a deferred capture contains the panicking frame, a
post-unwind capture does not.

FR-301's guarantee is scoped to "a panic on the job's own goroutine". The
job-authoring guide (#30) must tell users to recover inside any goroutine they
spawn.

---

### #19 — Per-attempt execution timeout

**Labels:** `area/executor` `phase/1` `type/feature` `priority/P1`
**Milestone:** Phase 1 — MVP
**Blocked by:** #18
**Traces:** FR-304, FR-305, AC-05

**Acceptance criteria**

- [ ] `context.WithTimeout` per attempt, default 30 minutes
- [ ] Configurable per job via `WithTimeout`
- [ ] Timeout recorded as a distinct outcome, not a generic failure
- [ ] `WARN` when an execution outlives its timeout by a wide margin
- [ ] Test: timed-out job does not delay dispatch of others (AC-05)

**Notes**

We cancel the context; we cannot forcibly kill a goroutine. A job that ignores
its context runs forever (SDS §12 row 12). "Respect your context" belongs in the
job-authoring guide.

The 30-minute default is deliberately long — cron jobs often are. The point is a
ceiling, not a tight bound.

---

### #20 — Retry with exponential backoff

**Labels:** `area/executor` `phase/1` `type/feature` `priority/P1` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #19, #8
**Traces:** FR-306, FR-307

**Acceptance criteria**

- [ ] `base * 2^attempt` with jitter, capped; defaults `base=1s`,
      `max=5 attempts`, `cap=5m`
- [ ] Configurable per job via `WithRetry`
- [ ] **Retries abort immediately if leadership is lost mid-sequence** (FR-307)
- [ ] Retries are in-memory; documented as lost on process restart
- [ ] Test: demotion mid-retry stops further attempts

**Notes**

Aborting on demotion is the safety-critical part — continuing to retry after
demotion means two replicas are working the same fire time.

Durable retry is deliberately out of scope: it means a queue table, and that is
River's product (SRS §7).

The cap is unreachable at the defaults (5 attempts from 1s peaks at 16s). It
exists to bound user-supplied configurations, not the default one.

---

### #21 — Deterministic idempotency key

**Labels:** `area/executor` `phase/1` `type/feature` `priority/P1` `good-first-issue`
**Milestone:** Phase 1 — MVP
**Blocked by:** #18
**Traces:** FR-314

```
sha256("d-cron:v1:" + namespace + ":" + jobName + ":" + fireTime.UTC().Format(RFC3339))
```

**Acceptance criteria**

- [ ] Computed per execution, injected into the job's context
- [ ] `dcron.IdempotencyKey(ctx) string` accessor
- [ ] **Identical across replicas for the same fire time** — this is the whole
      point
- [ ] Documented with a worked example using a payment provider's idempotency
      header

---

### #22 — Graceful shutdown with bounded drain

**Labels:** `area/executor` `area/api` `phase/1` `type/feature` `priority/P1`
**Milestone:** Phase 1 — MVP
**Blocked by:** #19, #11
**Traces:** FR-315, FR-303

**Acceptance criteria**

- [ ] `WithDrainTimeout`, default **30s** (inside Kubernetes' default grace
      period)
- [ ] `Stop(ctx)` waits for in-flight jobs up to the budget, then cancels their
      contexts
- [ ] Every job context is cancelled on shutdown regardless (FR-303)
- [ ] Ordering: unlock (#11) → drain → close
- [ ] Test: `Stop` returns within the budget even with a stuck job

**Notes**

Without a bounded drain, a single stuck 30-minute job hangs `SIGTERM` until the
orchestrator `SIGKILL`s the pod. The flagship README example must not pass
`context.Background()` here.

---

### API and wiring

---

### #23 — Public API: `Scheduler`, functional options, job registration

**Labels:** `area/api` `phase/1` `type/feature` `priority/P0`
**Milestone:** Phase 1 — MVP
**Blocked by:** #17, #20, #12
**Traces:** FR-201, FR-203, FR-601, FR-602, FR-603, NFR-301

**Acceptance criteria**

- [ ] `New(db *sql.DB, opts ...Option)`; `Add`, `Start`, `Stop`
- [ ] `JobFunc = func(context.Context) error` — **no payloads, no
      serialisation** (that road leads to being a task queue)
- [ ] Duplicate job names rejected at registration with a descriptive error
      (FR-203)
- [ ] All options from SDS §8 present with documented defaults
- [ ] Library never calls `os.Exit`, never panics at package level, never writes
      to stdout (FR-603)
- [ ] Minimal integration is ≤10 lines (NFR-301) and is covered by an
      example test
- [ ] `v0.x` in all version references until the epoch design has production
      mileage

---

### #24 — `pgx` support alongside `database/sql`

**Labels:** `area/api` `phase/1` `type/feature` `priority/P1`
**Milestone:** Phase 1 — MVP
**Blocked by:** #23
**Traces:** FR-506, NFR-403

**Acceptance criteria**

- [ ] `NewWithPool(pool *pgxpool.Pool, opts ...Option)`
- [ ] Internal `conn` / `Querier` interfaces so elector and store are
      driver-agnostic
- [ ] Both drivers exercised in the integration suite
- [ ] `pgx` dependency isolated so `database/sql` users don't link it

**Notes**

This closes SDS open question O-4. Both FR-506 and NFR-403 are Phase 1, so this
cannot remain an open question — a P1 requirement can't rest on an undecided
design point.

---

### #25 — Structured logging via `log/slog`

**Labels:** `area/observability` `phase/1` `type/feature` `priority/P1` `good-first-issue`
**Milestone:** Phase 1 — MVP
**Blocked by:** #23
**Traces:** FR-401, FR-402, NFR-501

**Acceptance criteria**

- [ ] Accepts a `*slog.Logger`; sensible default; no framework imposed
- [ ] Every leadership transition logged at `INFO` with instance ID and
      resolved lock key
- [ ] Job start/finish/failure at appropriate levels
- [ ] **Never logs connection strings, credentials, or job arguments**
      (NFR-501)
- [ ] Test asserting no credential material appears in output

**Notes**

Leadership transition lines are what someone reads at 3am. Make them
self-explanatory.

---

### #26 — Typed, actionable errors

**Labels:** `area/api` `phase/1` `type/feature` `priority/P2` `good-first-issue`
**Milestone:** Phase 1 — MVP
**Blocked by:** #23
**Traces:** NFR-303

**Acceptance criteria**

- [ ] `errors.go` with typed errors for: duplicate job name, invalid schedule,
      pool too small, missing session-stability assertion, panic, timeout
- [ ] Every error names the affected job or configuration key
- [ ] `errors.Is` / `errors.As` friendly

---

### #27 — Single-replica parity

**Labels:** `area/api` `phase/1` `type/feature` `priority/P1`
**Milestone:** Phase 1 — MVP
**Blocked by:** #23
**Traces:** FR-604, AC-08

**Acceptance criteria**

- [ ] One replica behaves exactly as an in-process cron — no surprises, no extra
      latency before the first fire
- [ ] Test comparing fire times against `robfig/cron` for identical specs
      (AC-08)

---

### Testing

---

### #28 — Integration test suite (elector)

**Labels:** `area/testing` `phase/1` `type/chore` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #10, #11, #13, #14
**Traces:** NFR-204, AC-02, AC-02b, AC-07, AC-09

**Acceptance criteria**

- [ ] N schedulers, one leader, N−1 standbys
- [ ] Killing the leader's connection promotes exactly one standby
- [ ] Explicit unlock releases promptly
- [ ] Refuses to start: no session-stability assertion; `MaxOpenConns(1)`
- [ ] **C-07 regression**: double `try_lock` returns true twice; single unlock
      leaves it held
- [ ] **PgBouncer regression**: `pg_backend_pid()` probe fails to distinguish
      transaction pooling; orphaned lock survives client disconnect; two clients
      acquire the same key
- [ ] **AC-02b**: with keepalives disabled, a partitioned leader's lock is
      *not* released promptly — asserting the limitation so it can't be
      quietly overstated later
- [ ] **AC-09**: zero tables exist in any schema after a full Phase 1 lifecycle

---

### #29 — Five-replica soak test

**Labels:** `area/testing` `phase/1` `type/chore` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #28, #27
**Traces:** AC-01, AC-03, AC-06, NFR-104, NFR-203

The artifact that substantiates the project's claims. Runs nightly; results
published in the README.

**Acceptance criteria**

- [ ] 5 replicas, a job firing every minute, external ledger counting executions
- [ ] **Exactly one execution per minute** across: steady state (30 min),
      leader `SIGKILL` **on a live host**, rolling restart, database restart
- [ ] AC-06: with 200 jobs across 5 replicas, `d-cron` query volume stays O(1)
      per interval with no spike at minute boundaries
- [ ] **NFR-104**: benchmark asserting steady-state memory overhead under 10 MB
      with 1,000 registered jobs
- [ ] **NFR-203**: no replica is structurally required — the test kills each
      replica in turn, including the original leader, and the schedule survives
      every one
- [ ] Nightly CI schedule with results published

**Notes**

⚠️ **Do not assert exactly-one under induced `SIGSTOP` pauses.** That scenario
can legitimately duplicate (SDS §12 row 6), and asserting otherwise would encode
the exactly-once claim SRS §6.2 forbids. The `SIGSTOP` case belongs in #41 with
weaker, different assertions.

---

### Docs

---

### #30 — README, correctness model, and job-authoring guide

**Labels:** `area/docs` `phase/1` `type/chore` `priority/P0` `safety-critical`
**Milestone:** Phase 1 — MVP
**Blocked by:** #23
**Traces:** NFR-304, §6, §12

The README is where this project's credibility is won or lost. Lead with the
honest limitations, not the pitch.

**Acceptance criteria**

- [ ] Correctness model stated plainly: at-most-once normally, at-least-once
      under failure *only with catch-up enabled*, **never "exactly-once"**
- [ ] Full failure-mode table (SDS §12), including host death, partition, and
      the goroutine-panic limitation
- [ ] **Required keepalive settings in the quickstart**, not an appendix
- [ ] Default `MissedSkip` behaviour stated: a missed fire time produces
      **zero** executions
- [ ] Phase 1/2 gap noted: skipped fires are logged but not durably counted
      until Phase 3
- [ ] Job-authoring guide: idempotency, respect your context, recover in
      spawned goroutines
- [ ] Comparison section written **from SRS Appendix A**, honouring its
      "claims that must not be made" list

**Notes**

Appendix A of the SRS is load-bearing and already verified (2026-08-13). Do not
redo that research; do read it before writing any competitor comparison.
Particularly: `gocron` v2 **does** have a `DistributedElector`, and `libi/dcron`
uses consistent hashing, **not** per-job locking.

---

### #31 — Migration guide from `robfig/cron`

**Labels:** `area/docs` `phase/1` `type/chore` `priority/P1` `good-first-issue`
**Milestone:** Phase 1 — MVP
**Blocked by:** #30
**Traces:** NFR-302

**Acceptance criteria**

- [ ] Step-by-step before/after code
- [ ] Mapping table for `robfig/cron` options
- [ ] Behavioural differences called out explicitly
- [ ] Note that jobs must be made idempotent

---

### #32 — Examples: minimal and Kubernetes

**Labels:** `area/docs` `phase/1` `type/chore` `priority/P1` `good-first-issue`
**Milestone:** Phase 1 — MVP
**Blocked by:** #23
**Traces:** NFR-301, A-05

**Acceptance criteria**

- [ ] `examples/minimal` — the ≤10-line integration
- [ ] `examples/kubernetes` — 5-replica manifest reproducing AC-01…AC-03,
      including required keepalive configuration
- [ ] Both compile in CI

---

## Phase 2 — Observability (8 issues)

---

### #33 — One-off jobs (`AddOnce`)

**Labels:** `area/clock` `phase/2` `type/feature` `priority/P2`
**Milestone:** Phase 2 — Observability
**Blocked by:** #16
**Traces:** FR-209

**Acceptance criteria**

- [ ] `onceSchedule` fires at one instant, then returns the zero time
- [ ] Heap evicts entries returning the zero time
- [ ] `AddOnce(name, at, fn)` on the scheduler
- [ ] Documented as re-registered on restart — not persisted in Phase 2

---

### #34 — Store package with idempotent migrations

**Labels:** `area/store` `phase/2` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 2 — Observability
**Blocked by:** #24
**Traces:** FR-502, FR-503, FR-504, NFR-503

**Acceptance criteria**

- [ ] Opt-in via `WithHistory(retention)`; nothing created unless enabled
- [ ] Schema `dcron` by default, configurable — **never `public`**
- [ ] `IF NOT EXISTS` DDL in a transaction, guarded by a **separate advisory
      lock** so N replicas starting at once don't race (FR-504)
- [ ] No migration framework — the schema is two tables
- [ ] **All queries parameterised** (NFR-503); schema/table identifiers
      validated against an allowlist rather than interpolated, since
      `WithSchema` is user-supplied
- [ ] `golangci-lint` SQL-injection linter enabled for this package
- [ ] Test: 10 replicas migrating concurrently from empty

---

### #35 — Execution history recording and retention pruning

**Labels:** `area/store` `phase/2` `type/feature` `priority/P1`
**Milestone:** Phase 2 — Observability
**Blocked by:** #34
**Traces:** FR-502, FR-505

**Acceptance criteria**

- [ ] `dcron.execution` written per execution with all statuses:
      `running|success|failed|panicked|skipped|timeout`
- [ ] Index on `(namespace, job_name, scheduled_at DESC)`
- [ ] Retention pruning runs on the leader as an internal `d-cron` job
- [ ] History write failure never fails the job itself

---

### #36 — Prometheus metrics subpackage

**Labels:** `area/observability` `phase/2` `type/feature` `priority/P1`
**Milestone:** Phase 2 — Observability
**Blocked by:** #35
**Traces:** FR-403, FR-404, NFR-402

**Acceptance criteria**

- [ ] Separate `metrics/` package — core must not link
      `prometheus/client_golang` (NFR-402)
- [ ] Caller-supplied registry, not the global default (FR-404)
- [ ] All metrics from SDS §11: `dcron_is_leader`,
      `dcron_leader_transitions_total`, `dcron_job_executions_total{job,status}`,
      `dcron_job_duration_seconds{job}`, `dcron_job_last_success_timestamp{job}`,
      `dcron_jobs_running{job}`, `dcron_fenced_writes_total`,
      `dcron_missed_runs_total{job}`
- [ ] **Documented alert rules with duration qualifiers** — see notes
- [ ] Binary-size test proving core doesn't pull the Prometheus dependency

**Notes**

Do not document `sum(dcron_is_leader) != 1` as an alert. It is legitimately 0
for a poll interval on every normal failover, 0 while the database is
unreachable, and 2 during the split-brain window. Alerting on the instantaneous
value pages on healthy failover. Ship these instead:

```promql
sum(dcron_is_leader) == 0
  for: 2m
sum(dcron_is_leader) > 1
  for: 30s
increase(dcron_fenced_writes_total[5m]) > 0
```

---

### #37 — `Leadership()` three-state accessor and health probe

**Labels:** `area/api` `area/observability` `phase/2` `type/feature` `priority/P1`
**Milestone:** Phase 2 — Observability
**Blocked by:** #8
**Traces:** FR-109, FR-411

**Acceptance criteria**

- [ ] `LeadershipState` enum: `LeadershipUnknown | LeadershipStandby |
      LeadershipLeader`
- [ ] `Leadership() LeadershipState` — **not** an `IsLeader() bool`
- [ ] `HealthCheck(ctx) error` reporting coordination-backend reachability,
      suitable as a Kubernetes probe
- [ ] `Jobs() []JobStatus` with last outcome, duration, next run

**Notes**

A bool collapses "not leader" and "don't know" — exactly the distinction a
readiness probe needs. FR-109 requires all three states, and the elector state
machine (#8) has a real `UNKNOWN`.

---

### #38 — Embedded web dashboard

**Labels:** `area/observability` `phase/2` `type/feature` `priority/P1`
**Milestone:** Phase 2 — Observability
**Blocked by:** #35, #37
**Traces:** FR-405, FR-406, FR-407, FR-408, NFR-502

**Acceptance criteria**

- [ ] Plain `http.Handler`, mountable at any path (FR-405)
- [ ] Shows: leadership state + instance ID, **resolved lock key**, registered
      jobs with schedules, last outcome and duration, next run, recent history
      (FR-406)
- [ ] Server-rendered `html/template`; assets via `embed.FS`; **no CDN, no build
      step, works air-gapped** (FR-407)
- [ ] **Read-only** in Phase 2 (NFR-502)
- [ ] Off by default; docs state clearly that authentication is the host app's
      responsibility (FR-408)

**Notes**

Showing the resolved lock key matters — namespace collision is otherwise very
hard to diagnose.

Not a differentiator on its own: `gocron-ui` already exists. Don't oversell it.

---

### #39 — Failure notification hook

**Labels:** `area/observability` `phase/2` `type/feature` `priority/P2` `good-first-issue`
**Milestone:** Phase 2 — Observability
**Blocked by:** #35
**Traces:** FR-409

**Acceptance criteria**

- [ ] Hook interface invoked on terminal job failure (after retries exhausted)
- [ ] Webhook implementation provided
- [ ] Hook failures logged, never propagated to the job
- [ ] Hook invocation is asynchronous and bounded

---

### #40 — Phase 2 docs update

**Labels:** `area/docs` `phase/2` `type/chore` `priority/P2`
**Milestone:** Phase 2 — Observability
**Blocked by:** #36, #38
**Traces:** NFR-304, NFR-405

**Acceptance criteria**

- [ ] Metrics reference with the alert rules from #36
- [ ] Dashboard mounting and auth guidance
- [ ] History schema documented, including that enabling it creates tables
      (the "zero migrations" claim is Phase 1 only — keep the qualification)
- [ ] Full godoc coverage for exported identifiers (NFR-405)

---

## Phase 3 — Correctness hardening (8 issues)

---

### #41 — `leader_epoch` table and monotonic increment

**Labels:** `area/elector` `area/store` `phase/3` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #34, #8
**Traces:** FR-110, FR-507

```sql
INSERT INTO dcron.leader_epoch (namespace, epoch, instance_id, acquired_at)
VALUES ($1, 1, $2, now())
ON CONFLICT (namespace) DO UPDATE
  SET epoch       = dcron.leader_epoch.epoch + 1,
      instance_id = EXCLUDED.instance_id,
      acquired_at = now()
RETURNING epoch;
```

**Acceptance criteria**

- [ ] Epoch incremented and read back on every successful acquisition
- [ ] Persisted so monotonicity survives a full cluster restart (FR-507)
- [ ] Test: 20 sequential promotions yield strictly increasing epochs
- [ ] Test: monotonicity holds across a complete restart

**Notes**

An advisory lock provides no version number, so a real monotonic source is
required. An in-memory counter would reset to 1 on full restart, letting a
long-stalled zombie from the previous cluster generation look current.

The statement is safe under concurrency because only the lock holder runs it.
Verified to execute correctly on PostgreSQL 16.

---

### #42 — Epoch in context and fenced `d-cron` writes

**Labels:** `area/executor` `area/store` `phase/3` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #41, #35
**Traces:** FR-309, FR-310

**Acceptance criteria**

- [ ] Epoch injected into every job context; `dcron.Epoch(ctx) int64` accessor
- [ ] **Both** the opening `status = running` insert and the terminal update are
      *guarded*, not merely stamped
- [ ] Zero rows affected ⇒ log `WARN`, increment `dcron_fenced_writes_total`,
      discard
- [ ] Test: a write from a demoted leader affects zero rows

**Notes**

FR-310 says *all* `d-cron`-owned writes. An earlier design guarded only the
update, leaving the insert to carry an unvalidated epoch. Use the
`INSERT ... SELECT ... WHERE EXISTS (SELECT 1 FROM dcron.leader_epoch WHERE
namespace = $1 AND epoch = $5)` form from SDS §6.2.

---

### #43 — `Fence()` helper for user transactions

**Labels:** `area/api` `phase/3` `type/feature` `priority/P0` `safety-critical`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #42
**Traces:** FR-311, NFR-403

```go
func Fence(ctx context.Context, tx Querier) error
```

**Acceptance criteria**

- [ ] Uses `SELECT epoch FROM dcron.leader_epoch WHERE namespace = $1 FOR SHARE`
- [ ] `Querier` satisfied by both `*sql.Tx` and `pgx.Tx` (NFR-403)
- [ ] **Concurrency test**: promotion committing between the fence read and the
      user's commit must abort the zombie's transaction
- [ ] Documented honestly: only same-database transactional effects can be
      fenced; an HTTP call to a payment provider cannot

**Notes**

⚠️ **The lock mode is load-bearing.** A plain `SELECT epoch` has a TOCTOU hole:
under the default READ COMMITTED, a promotion that commits *after* our read but
*before* the user's `tx.Commit()` is invisible, and the zombie's charge commits
unfenced. `FOR SHARE` makes the new leader's epoch `UPDATE` conflict.

`FOR SHARE` rather than `FOR UPDATE` so concurrent fenced jobs under the same
epoch don't serialise against each other — only against an actual leadership
change.

---

### #44 — Overlap policy

**Labels:** `area/executor` `phase/3` `type/feature` `priority/P1`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #35
**Traces:** FR-308

**Acceptance criteria**

- [ ] `OverlapSkip` (default), `OverlapQueue` (depth 1), `OverlapAllow`
- [ ] Skipped fires **recorded and counted**, never silently dropped
- [ ] `dcron_jobs_running{job}` reflects concurrency accurately
- [ ] Tests for all three policies with a deliberately slow job

**Notes**

Skip is the default because it matches what people want from an "every 5
minutes" job that occasionally takes 7. A job quietly skipping half its runs is
exactly the sort of thing that goes unnoticed for months — hence the recording
requirement.

---

### #45 — Missed-run catch-up with hard caps

**Labels:** `area/clock` `area/store` `phase/3` `type/feature` `priority/P1` `safety-critical`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #35
**Traces:** FR-312, FR-313

**Acceptance criteria**

- [ ] `MissedSkip` (default) and `MissedCatchUp`
- [ ] `MissedCatchUp` requires history; **rejected at configuration time** if
      history is disabled, never silently no-op
- [ ] Configurable maximum lookback window
- [ ] Configurable cap on catch-up runs dispatched per job (FR-313)
- [ ] `dcron_missed_runs_total{job}` incremented under both policies
- [ ] Test: a weekend-long outage with an hourly job does not fire 48 executions
      in a burst

**Notes**

Catch-up is dangerous by surprise — a report emailed 7 hours late may be worse
than not sent. Hence opt-in plus hard caps.

---

### #46 — Interval-since-last-success scheduling

**Labels:** `area/clock` `phase/3` `type/feature` `priority/P2`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #35, #16
**Traces:** FR-210

**Acceptance criteria**

- [ ] `sinceSuccessSchedule`: next = completion of last success + d
- [ ] `WithSinceLastSuccess(d)` job option
- [ ] On promotion, the new leader reads last-success from history
- [ ] Configuration rejected if history is disabled — no silent fallback
- [ ] Test: a job failing repeatedly does not drift its schedule unexpectedly

**Notes**

The only stateful schedule type, which is why it sits in Phase 3 behind the
store rather than shipping with the others.

---

### #47 — OpenTelemetry tracing subpackage

**Labels:** `area/observability` `phase/3` `type/feature` `priority/P2`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #35
**Traces:** FR-410, NFR-402

**Acceptance criteria**

- [ ] Separate `otel/` package; core does not link the OTel SDK
- [ ] Span per execution when a tracer provider is supplied
- [ ] Span attributes: job name, scheduled time, attempt, epoch, outcome
- [ ] No-op when no provider is configured

---

### #48 — Phase 3 split-brain chaos test

**Labels:** `area/testing` `phase/3` `type/chore` `priority/P0` `safety-critical`
**Milestone:** Phase 3 — Correctness hardening
**Blocked by:** #43
**Traces:** §6.2, §12 row 6

Deliberately synthesise the split-brain window with `SIGSTOP` and assert the
*correct*, weaker guarantees.

**Acceptance criteria**

- [ ] `SIGSTOP` the leader long enough to drop its connection; resume it after
      a standby is promoted
- [ ] At most one **committed** `d-cron` execution record per fire time
- [ ] `dcron_fenced_writes_total` increments (proving fencing engaged)
- [ ] The zombie's `Fence()`d transaction aborted
- [ ] Duplicate *invocations* recorded as **expected behaviour, not failure**

**Notes**

⚠️ This test must **not** assert exactly-once. SRS §6.2 explicitly disowns that
guarantee, and encoding it here would either fail spuriously or bake in a claim
we've said we won't make.

---

## Phase 4 — Extensibility (4 issues)

---

### #49 — Extract `Coordinator` interface

**Labels:** `area/elector` `phase/4` `type/feature` `priority/P1`
**Milestone:** Phase 4 — Extensibility
**Blocked by:** #41
**Traces:** FR-111

**Acceptance criteria**

- [ ] Coordination abstracted so scheduling logic is backend-agnostic
- [ ] PostgreSQL advisory-lock implementation moved behind it with no
      behaviour change
- [ ] Interface documents the epoch and liveness contract a backend must satisfy
- [ ] Full existing test suite passes unchanged

**Notes**

Worth revisiting SDS O-9 here: a leader-written liveness timestamp that standbys
read would make host-death failover independent of operator keepalive
configuration — currently the weakest link in the design (C-06).

---

### #50 — Runtime job management

**Labels:** `area/api` `area/store` `phase/4` `type/feature` `priority/P2`
**Milestone:** Phase 4 — Extensibility
**Blocked by:** #49
**Traces:** FR-211

**Acceptance criteria**

- [ ] Add, pause, resume, remove jobs without restart
- [ ] Changes propagate to a newly promoted leader
- [ ] Dashboard reflects runtime state
- [ ] Concurrency-safe against the running scheduler loop

---

### #51 — Administrative API

**Labels:** `area/api` `phase/4` `type/feature` `priority/P2`
**Milestone:** Phase 4 — Extensibility
**Blocked by:** #50
**Traces:** FR-605, NFR-502

**Acceptance criteria**

- [ ] REST or gRPC surface for job management
- [ ] **Gated behind explicit opt-in**, off by default
- [ ] Authentication requirements documented; mutating surface requires it
      (NFR-502)
- [ ] OpenAPI or protobuf definition published

---

### #52 — Alternative coordination backends

**Labels:** `area/elector` `phase/4` `type/feature` `priority/P2` `help-wanted`
**Milestone:** Phase 4 — Extensibility
**Blocked by:** #49
**Traces:** FR-111

**Acceptance criteria**

- [ ] At least one non-PostgreSQL backend (Redis or etcd) as a separate module
- [ ] Shared conformance test suite any backend must pass
- [ ] Documented trade-offs per backend
- [ ] Core module gains no new dependencies

**Notes**

Good candidate for outside contribution once the conformance suite exists. Also
the escape hatch for users stuck behind transaction-pooling proxies (C-01).

---

## Appendix A — Dependency graph

Critical path to a shippable v0.1.0:

```
#1 name
 └─ #2 scaffold
     ├─ #3 CI
     ├─ #4 pg harness ──┬─ #5 pgbouncer harness ─┐
     │                  │                        │
     ├─ #6 lock key ────┴─ #7 acquire ───────────┼─ #12 session gate ─┐
     │                        ├─ #8 state machine │                   │
     │                        │   ├─ #9 polling   │                   │
     │                        │   ├─ #10 liveness │                   │
     │                        │   └─ #11 unlock   │                   │
     │                        ├─ #13 pool check ──┤                   │
     │                        └─ #14 keepalives ──┤                   │
     │                                            │                   │
     ├─ #15 cron parser ─ #16 Schedule ─ #17 loop ─┼───────────────────┼─ #23 API
     │                                            │                   │    ├─ #24 pgx
     └─ #18 panic ─ #19 timeout ─ #20 retry ──────┘                   │    ├─ #25 slog
         └─ #21 idempotency key                                        │    ├─ #26 errors
         └─ #22 drain ─────────────────────────────────────────────────┘    └─ #27 parity
                                                                                  │
                              #28 integration tests ──── #29 soak test ───────────┤
                              #30 README ─ #31 migration guide ─ #32 examples ────┘
```

**Parallelisable from day one** (after #2): the clock chain (#15→#17), the
executor chain (#18→#22), and the elector chain (#6→#14) touch different
packages and can be worked by three people concurrently. They converge at #23.

**Longest pole:** #4 → #5 → #12 → #23 → #28 → #29. Land the test harnesses
early; everything safety-critical is blocked behind them.

---

## Appendix B — Requirement coverage matrix

Every FR and NFR in the SRS maps to at least one issue.

| Requirement group | Issues |
| :--- | :--- |
| FR-101…FR-108 (election) | #6, #7, #8, #9, #11, #12 |
| FR-109…FR-114 (election, cont.) | #8, #10, #14, #13, #37, #41, #49 |
| FR-201…FR-212 (scheduling) | #15, #16, #17, #23, #33, #46, #50 |
| FR-301…FR-315 (execution safety) | #18, #19, #20, #21, #22, #42, #43, #44, #45 |
| FR-401…FR-411 (observability) | #25, #36, #37, #38, #39, #47 |
| FR-501…FR-508 (persistence) | #7, #24, #34, #35, #41 |
| FR-601…FR-605 (API) | #23, #24, #26, #27, #51 |
| NFR-101…NFR-106 (performance) | #9, #17, #19, #29 |
| NFR-201…NFR-204 (reliability) | #28, #29, #30 |
| NFR-301…NFR-304 (usability) | #23, #26, #30, #31, #32 |
| NFR-401…NFR-405 (maintainability) | #2, #3, #15, #24, #36, #40, #47 |
| NFR-501…NFR-504 (security) | #2, #3, #25, #38, #51 |
| AC-01…AC-10 (acceptance) | #27, #28, #29 |
| C-01, C-06, C-07 (constraints) | #5, #10, #12, #14, #28 |

Verified programmatically: **all 65 FRs, all 23 NFRs and all 11 ACs defined in
the SRS are cited by at least one issue**, no issue cites a requirement ID that
doesn't exist, and no `Blocked by` references a non-existent issue.

C-02 through C-05 (PostgreSQL mandatory, in-process execution, locks are
per-database, read replicas can't participate) are scope statements rather than
work items, so they carry no issue — they are enforced by SRS §7 (Out of Scope)
and surface in #30's documentation.

**Safety-critical issues (19):** #5, #7, #8, #9, #10, #12, #14, #17, #18, #20,
#28, #29, #30, #34, #41, #42, #43, #45, #48 — require two approvals and a
linked test.

---

## Appendix C — Bulk create with `gh` CLI

Run from the repo root. Creates labels, milestones, then issues **in backlog
order**, so on a fresh repo the GitHub issue numbers match the `#N` references
above.

```bash
#!/usr/bin/env bash
set -euo pipefail

# --- labels ---
gh label create "area/elector"       --color 1D76DB --description "Leader election, locks" --force
gh label create "area/clock"         --color 0E8A16 --description "Schedules and fire loop" --force
gh label create "area/executor"      --color 5319E7 --description "Job invocation safety" --force
gh label create "area/store"         --color B60205 --description "Persistence and migrations" --force
gh label create "area/api"           --color FBCA04 --description "Public surface" --force
gh label create "area/observability" --color 006B75 --description "Logs, metrics, UI" --force
gh label create "area/testing"       --color BFD4F2 --description "Test harness and suites" --force
gh label create "area/docs"          --color C5DEF5 --description "Documentation" --force
for p in 0 1 2 3 4; do
  gh label create "phase/$p" --color EEEEEE --description "Roadmap phase $p" --force
done
gh label create "type/feature"    --color A2EEEF --force
gh label create "type/bug"        --color D73A4A --force
gh label create "type/chore"      --color FEF2C0 --force
gh label create "type/spike"      --color D4C5F9 --force
gh label create "priority/P0"     --color B60205 --force
gh label create "priority/P1"     --color D93F0B --force
gh label create "priority/P2"     --color FBCA04 --force
gh label create "safety-critical" --color 000000 --description "Two reviewers + linked test required" --force
gh label create "good-first-issue" --color 7057FF --force
gh label create "help-wanted"      --color 008672 --force

# --- milestones (gh has no native milestone create; use the API) ---
repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
for m in "Phase 0 — Bootstrap" "Phase 1 — MVP" "Phase 2 — Observability" \
         "Phase 3 — Correctness hardening" "Phase 4 — Extensibility"; do
  gh api "repos/$repo/milestones" -f title="$m" >/dev/null 2>&1 || true
done

# --- issues ---
# Split this document on the "### #N —" headings and create each in order.
# Body files are expected at .github/backlog/NN.md
for n in $(seq -w 1 52); do
  f=".github/backlog/${n}.md"
  [ -f "$f" ] || continue
  title=$(head -1 "$f")
  tail -n +2 "$f" > /tmp/body.md
  gh issue create --title "$title" --body-file /tmp/body.md
  sleep 1   # stay under secondary rate limits
done
```

To generate the per-issue body files from this document:

```bash
mkdir -p .github/backlog
awk '
  /^### #[0-9]+ — / {
    n = $2; gsub(/#/, "", n)
    file = sprintf(".github/backlog/%02d.md", n)
    title = $0; sub(/^### #[0-9]+ — /, "", title)
    print title > file
    next
  }
  /^## / { file = "" }
  file { print >> file }
' GITHUB-ISSUES.md
```

Review the generated files before running the create loop — the `gh issue
create` calls are not idempotent, and re-running duplicates every issue.

---

*Generated from DCRON-SRS-001 v1.0 and DCRON-SDS-001 v1.0, 2026-08-13.*
