# `d-cron` — Design Document

**DCRON-SDS-001 · v1.0 draft · 2026-08-13**
Companion to `SRS.md` (DCRON-SRS-001). Where the SRS says _what_, this says _how_ and _why_.

Audience: contributors. This reads as an RFC, not a specification — the
rationale matters more than the numbering, and dissent on any decision below is
welcome in the tracking issue.

---

## Contents

- [1. What we're building](#1-what-were-building)
- [2. Architecture](#2-architecture)
- [3. Leader election](#3-leader-election)
- [4. The scheduler loop](#4-the-scheduler-loop)
- [5. Execution pipeline](#5-execution-pipeline)
- [6. Epoch fencing](#6-epoch-fencing)
- [7. Missed runs and overlap](#7-missed-runs-and-overlap)
- [8. Public API](#8-public-api)
- [9. Package layout](#9-package-layout)
- [10. Database schema](#10-database-schema)
- [11. Observability](#11-observability)
- [12. Failure modes](#12-failure-modes)
- [13. Testing strategy](#13-testing-strategy)
- [14. Decisions and rejected alternatives](#14-decisions-and-rejected-alternatives)
- [15. Open questions](#15-open-questions)
- [16. Build order](#16-build-order)

---

## 1. What we're building

A Go library you import into your existing app. It decides _which replica_ runs
a scheduled job, using the PostgreSQL database you already have. No Redis, no
etcd, no separate deployment, no migration for the core path.

The whole design rests on one idea: **coordinate once, not per job.** One
advisory lock decides who runs the clock. Everything else — fencing, history,
metrics — hangs off that.

Three properties we refuse to trade away:

1. If the leader dies, another replica takes over. No designated scheduler pod.
2. Adding `d-cron` to an app requires no schema change (Phase 1).
3. We never claim exactly-once. See SRS §6.

> **Revision note (v1.0).** This document was reviewed against a live
> PostgreSQL 16 instance and a real PgBouncer 1.22 in `transaction` mode. Six
> claims in the first draft were wrong and are corrected here: the
> `pg_backend_pid()` pooler probe does not work (§3.4); advisory locks are not
> released on host death or partition (§3.1, §12 rows 3–4); standbys must not
> poll on a pooled connection (§3.3); advisory locks are re-entrant, so the
> liveness check must never re-acquire (§3.5); `Fence()` needs `FOR SHARE` to
> close a TOCTOU hole (§6.3); and the soak test must not assert exactly-once
> under induced pauses (§13). Each correction is marked in place so the wrong
> version is not reintroduced.

---

## 2. Architecture

```
┌─────────────────── replica 1 (LEADER) ────────────────────┐
│  app code                                                 │
│  ┌─────────────────────────────────────────────────────┐  │
│  │ d-cron.Scheduler                                    │  │
│  │                                                     │  │
│  │  ┌───────────┐   holds lock   ┌──────────────────┐  │  │
│  │  │  elector  │───────────────▶│ dedicated *sql.  │──┼──┼──▶ PG
│  │  │           │                │ Conn (reserved)  │  │  │   (lock)
│  │  └─────┬─────┘                └──────────────────┘  │  │
│  │        │ leader=true, epoch=7                       │  │
│  │        ▼                                            │  │
│  │  ┌───────────┐    fire     ┌──────────┐             │  │
│  │  │   clock   │────────────▶│ executor │──▶ your Go  │  │
│  │  │ (min-heap)│             │ (recover,│    func     │  │
│  │  └───────────┘             │  retry,  │             │  │
│  │                            │  timeout)│             │  │
│  │                            └────┬─────┘             │  │
│  │                                 │ optional          │  │
│  │                                 ▼                   │  │
│  │                           ┌──────────┐              │  │
│  │                           │  store   │──────────────┼──┼──▶ PG
│  │                           │ (history)│              │  │   (pool)
│  │                           └──────────┘              │  │
│  └─────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────┘

┌── replica 2 (STANDBY) ──┐   ┌── replica 3 (STANDBY) ──┐
│ elector polls every 5s  │   │ elector polls every 5s  │
│ clock NOT running       │   │ clock NOT running       │
└─────────────────────────┘   └─────────────────────────┘
```

Four components, deliberately small and independently testable:

| Component  | Job                                                        | Knows about      |
| :--------- | :--------------------------------------------------------- | :--------------- |
| `elector`  | Acquire/hold/release the lock, emit leadership transitions | PostgreSQL only  |
| `clock`    | Compute next fire times, emit fire events                  | Nothing external |
| `executor` | Run the func safely: recover, timeout, retry               | Nothing external |
| `store`    | Persist history (optional, Phase 2+)                       | PostgreSQL only  |

The `Scheduler` wires them together and owns the state machine. Nothing else
talks to the database.

---

## 3. Leader election

### 3.1 Why advisory locks

`pg_try_advisory_lock(key bigint)` gives us a lock that:

- lives in PostgreSQL's shared memory — **no table, no migration**
- is scoped to the **session**, so it disappears when the connection dies
- returns immediately (`true`/`false`), never blocks

That third property is why we use `pg_try_` and not `pg_advisory_lock` — a
blocking acquire in a standby would tie up a connection indefinitely and make
graceful failover impossible to reason about.

The second property is the prize, **but it is narrower than it first appears
and an earlier draft of this document overstated it.** PostgreSQL releases a
session advisory lock when the _backend process_ exits. That covers process
death on a live host: `SIGKILL` the leader and the kernel sends FIN/RST, the
backend exits, the lock frees, a standby takes over in one poll interval. No
heartbeat, no TTL, no lease renewal loop.

It does **not** cover host death or network partition. If the leader's machine
loses power, freezes, or is partitioned away, the PostgreSQL backend sits
blocked in `recv()` and never learns. Verified defaults on PostgreSQL 16:

```
tcp_keepalives_idle              = 0   -- defer to OS (~2h11m on Linux)
tcp_keepalives_interval          = 0
tcp_keepalives_count             = 0
client_connection_check_interval = 0   -- disabled
```

With those defaults the lock stays held for **hours**, and because no replica
can acquire it, **nothing runs at all.** That is a worse outage than the
duplicate-execution problem we set out to solve.

So keepalives are not an optional tuning note, they are a deployment
requirement (SRS FR-113, C-06). We must:

- document required settings prominently, ideally in the quickstart;
- read `current_setting('tcp_keepalives_idle')` and
  `client_connection_check_interval` at startup and log a loud `WARN` when both
  are `0`;
- prefer setting them on our own lock connection's DSN where the driver allows
  it, so we are not wholly dependent on server configuration.

Restating the guarantee honestly: _advisory locks give us free failure
detection for process death; host-level failure detection costs one line of
keepalive configuration and we must make sure users apply it._

### 3.2 The lock key

Advisory lock keys are a single `bigint` (or two `int32`s) in a global
namespace shared by everything touching that database. Collisions are silent
and would be miserable to debug, so we derive the key deterministically and
document it:

```go
// key = int64 from the first 8 bytes of sha256("d-cron:v1:" + namespace)
func lockKey(namespace string) int64 {
    sum := sha256.Sum256([]byte("d-cron:v1:" + namespace))
    return int64(binary.BigEndian.Uint64(sum[:8]))
}
```

`namespace` defaults to `"default"` and should be set per application and per
environment (`"billing-api:prod"`). Two different apps sharing a database with
the same namespace would fight over one lock and half of them would never
schedule anything — this is the single easiest way to misconfigure `d-cron`, so
the docs must lead with it and the UI must display the resolved key.

### 3.3 The reserved connection

Session-scoped means the lock lives on _one specific connection_. We cannot use
`db.Query(...)` from a pool — the pool may hand us a different connection next
time, on which we hold nothing.

So the elector calls `db.Conn(ctx)` and **holds that `*sql.Conn` for the entire
duration of leadership** (SRS FR-104).

Two precise consequences, both of which an earlier draft got wrong:

**The connection comes _from_ the caller's pool, it is not additional to it.**
So a caller who sized their pool for their own load silently loses one
connection while their replica is leader. We must document this (SRS FR-508),
not describe it as an overhead "beyond the pool" (SRS NFR-105 as corrected). If
the pool has `MaxOpenConns(1)` we deadlock, so we check `db.Stats().MaxOpenConnections`
at startup and refuse (SRS FR-112).

**Standbys must also poll on a dedicated `db.Conn()`, not a pooled query.** The
earlier draft said standbys "acquire one from the pool per poll and release it"
— that is unsafe and contradicts this very section. If a `pg_try_advisory_lock`
issued on a pooled connection _succeeds_, the lock lands on a session that is
then returned to the pool: we hold leadership on a connection we no longer
control, and releasing it later is not possible in the general case. The correct
shape is: acquire a `*sql.Conn`, attempt the lock on it, **close it immediately
on failure, retain it on success.**

### 3.4 The pooler problem

This is the biggest real-world adoption risk (SRS C-01), and it is worse than an
earlier draft of this document claimed — **both because the failure is nastier
and because the detection scheme proposed did not work.**

PgBouncer in `transaction` pooling mode — extremely common in Go+Postgres
shops — multiplexes many clients onto one server session. Measured against
PgBouncer 1.22:

| Test                                          | Result                                                              |
| :-------------------------------------------- | :------------------------------------------------------------------ |
| `pg_try_advisory_lock(k)` through the pooler  | `true`                                                              |
| Lock state after acquiring client disconnects | **still held** — orphaned on an idle pooled session, unreleasable   |
| A _second_ client acquires the same key `k`   | **`true`** — same server session, and advisory locks are re-entrant |

So the failure mode is not merely "the lock is unreliable." It is **two
simultaneous leaders plus a permanently orphaned lock.** Exactly what we exist
to prevent.

#### Why the `pg_backend_pid()` probe was wrong

The earlier draft proposed calling `pg_backend_pid()` twice on one `*sql.Conn`
and treating differing PIDs as proof of transaction pooling. Measured, this
returns the **same** PID on every attempt through PgBouncer in `transaction`
mode: the pooler only reassigns server sessions when the pool is _contended_,
and at application startup — the one moment we run the check — it is not. A
false negative, reliably, in the exact conditions we care about. The
`set_config` + read-back variant fails identically for the same reason.

`SHOW pool_mode` is likewise useless: PgBouncer forwards it to the server, which
answers `ERROR: unrecognized configuration parameter "pool_mode"` — byte
identical to direct PostgreSQL. `pool_mode` is only visible on PgBouncer's
_admin_ console via a separate `dbname=pgbouncer` DSN, and even there it is a
field of `SHOW CONFIG`, not a `SHOW pool_mode` statement.

#### What we do instead

We stop pretending to detect it and make the operator declare it (SRS FR-108):

```go
// Refuses to start without one of these:
dcron.WithSessionStableConnection()      // operator asserts direct / session-mode
dcron.WithDedicatedLockDSN(dsn string)   // d-cron opens its own direct connection
```

Startup fails with an error that names the hazard, describes both observed
failure modes, and lists the remedies: point `d-cron` at a direct connection via
`WithDedicatedLockDSN`, switch the pooler to `session` mode, or wait for a
non-advisory-lock backend in Phase 4.

This is less magical than automatic detection and it is the honest option. A
detection scheme that silently passes in the dangerous case is worse than no
detection at all, because it converts an operator decision into a false
assurance.

### 3.5 State machine

```
                  ┌──────────────┐
     start ──────▶│   UNKNOWN    │
                  └──────┬───────┘
                         │ try_lock
              ┌──────────┴──────────┐
         true │                     │ false
              ▼                     ▼
       ┌────────────┐        ┌────────────┐
       │   LEADER   │        │  STANDBY   │
       │ clock ON   │        │ clock OFF  │
       │ epoch = n  │        │            │
       └──────┬─────┘        └──────┬─────┘
              │ conn lost /         │ poll every
              │ Stop() /            │ interval+jitter
              │ ping fails          │ → try_lock
              ▼                     │
       ┌────────────┐               │
       │ DEMOTING   │◀──────────────┘ (on success: epoch++)
       │ drain jobs │
       └──────┬─────┘
              ▼
          STANDBY
```

Standby poll interval defaults to 5s with ±20% jitter. Jitter matters: without
it, all N standbys wake in lockstep and we reintroduce a small thundering herd
at exactly the moment a leader dies — the worst possible time.

The leader also verifies liveness on its reserved connection each interval.
Holding the lock is not the same as _knowing_ you hold it; a leader whose
connection has quietly gone away must find out.

> **This check must never re-acquire the lock** (SRS FR-114, C-07). Advisory
> locks are re-entrant: two `pg_try_advisory_lock(k)` calls on one session both
> return `true` and increment an internal counter, and a single
> `pg_advisory_unlock(k)` then leaves the lock **still held**. A liveness check
> implemented as a re-`try_lock` — a natural and tempting reading — would
> increment that counter every 5s, and §3.6's explicit unlock on shutdown would
> silently fail to release, degrading failover to the connection-teardown path
> this design exists to avoid. The check is a plain `SELECT 1`, optionally
> corroborated by a `pg_locks` lookup for our key and PID.

### 3.6 Release on shutdown

On `SIGTERM`/`Stop()` we call `pg_advisory_unlock(key)` explicitly, then close
the connection.

An earlier draft justified this by claiming socket teardown "could take tens of
seconds" — that is wrong for the graceful case. When the process exits, the
kernel sends FIN immediately and the backend exits in milliseconds, so the lock
would free promptly anyway. The genuine reasons to unlock explicitly are: it
releases the lock _before_ we begin draining in-flight jobs, which shortens the
window measurably when drain takes a while; it works when `Stop()` is called
without the process exiting (tests, embedded restarts, leadership handoff); and
it makes the intent explicit rather than relying on driver and OS behaviour we
do not control.

Explicit unlock does **nothing** for host death or partition (§3.1, SRS C-06) —
the process is gone and cannot issue the call. That case is entirely on
keepalive configuration.

---

## 4. The scheduler loop

Only the leader runs a clock. The design is a min-heap of next-fire-times, same
shape as `robfig/cron` — this is well-trodden and we shouldn't be creative:

```go
type entry struct {
    job      *Job
    next     time.Time
    schedule Schedule   // interface: Next(time.Time) time.Time
}
```

The loop sleeps until `heap.Peek().next`, wakes, dispatches everything due,
recomputes, re-heaps. Notes:

**Dispatch is asynchronous.** Each due job goes to `go executor.Run(...)`. The
loop must never block on job execution or a single slow job delays every other
job (SRS NFR-106, FR-305).

**Timezone is resolved once** at construction into a `*time.Location`, default
UTC. Per-job timezones are deliberately not supported — DST transitions with
mixed timezones produce genuinely confusing skipped and doubled fire times, and
the feature isn't worth the support burden.

**Fire times come only from the leader's clock** (SRS FR-208). We never read
`now()` from PostgreSQL for scheduling, never compare timestamps between
replicas. This is our answer to `libi/dcron`'s clock-skew objection to
lock-based approaches: skew between replicas cannot cause a double fire,
because only one replica's clock is ever consulted. Skew makes jobs fire a few
seconds early or late relative to wall-clock truth. We accept that and say so.

**On promotion, `next` is computed forward from `time.Now()`** — the new leader
does not attempt to reconstruct history. Anything missed during the gap is the
missed-run policy's problem (§7), not the clock's.

**The `Schedule` interface covers four implementations**, not just cron. All
four satisfy `Next(time.Time) time.Time`, so the heap does not care which it is:

| Impl                   | Requirement    | Phase | Note                                                                                                                                                                                                                                                                            |
| :--------------------- | :------------- | :---- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `cronSchedule`         | FR-202, FR-212 | P1    | 5-field, or 6-field with seconds opt-in                                                                                                                                                                                                                                         |
| `intervalSchedule`     | FR-205         | P1    | `@every 30s`; next = last fire + d                                                                                                                                                                                                                                              |
| `onceSchedule`         | FR-209         | P2    | Fires at one instant, then returns the zero time and is evicted from the heap. Needs no persistence in P2 — a one-off registered in code is re-registered on restart                                                                                                            |
| `sinceSuccessSchedule` | FR-210         | P3    | Next = **completion of last success** + d. Requires the P2 store to know when that was; on promotion the new leader reads last-success from history. Falls back to "now + d" if history is disabled, and configuration must reject the combination rather than silently degrade |

`sinceSuccessSchedule` is the only one that is stateful, and it is the reason
FR-210 sits in P3 behind the store rather than shipping alongside the other
schedule types.

**Schedule parsing.** We need 5-field cron, 6-field with seconds (opt-in),
`@every 30s`, and the `@daily`-style descriptors. Writing a cron parser is
about 200 lines and a well-understood problem; vendoring one avoids a
dependency in the core path (SRS NFR-401). Leaning toward writing it, with a
thorough table-driven test suite lifted from the spec — see
[open question O-1](#15-open-questions).

---

## 5. Execution pipeline

Every invocation passes through the same wrapper. Order matters:

```
fire event
   │
   ├─▶ overlap check (P3) ──────── skip? ──▶ record skipped, done
   │
   ├─▶ build context: cancellable, timeout, epoch, idempotency key
   │
   ├─▶ attempt loop (1..maxAttempts)
   │      │
   │      ├─▶ defer recover()  ◀── panic becomes error + stack trace
   │      ├─▶ job.Fn(ctx)
   │      ├─▶ success? ──▶ record, done
   │      └─▶ failure? ──▶ backoff, check still-leader, retry
   │
   └─▶ record terminal outcome, fire hooks
```

### 5.1 Panic recovery

Non-negotiable (SRS FR-301). A panicking job must not take down an HTTP server
serving live traffic:

```go
func (e *executor) invoke(ctx context.Context, j *Job) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = &PanicError{Value: r, Stack: debug.Stack()}
        }
    }()
    return j.Fn(ctx)
}
```

The stack must be captured _inside_ the deferred func, before the stack unwinds
further, or it's useless — verified: a deferred capture contains the panicking
frame, a post-unwind capture does not.

**The boundary has a real limit we must document, not imply away.** A panic on a
goroutine the job function itself spawns cannot be recovered here — Go
terminates the process, and no `defer` of ours is on that stack. So SRS FR-301's
guarantee is scoped to "a panic on the job's own goroutine" (§12 row 13). The
job-authoring guide must tell users to recover inside any goroutine they spawn.

### 5.2 Timeout

`context.WithTimeout` per attempt, default 30 minutes (long, because cron jobs
often are; the point is a ceiling, not a tight bound). We cancel the context —
we cannot forcibly kill a goroutine. A job that ignores its context will run
forever, and we log a warning when an execution outlives its timeout by a wide
margin. Documenting "respect your context" is part of the job-authoring guide.

### 5.3 Retry

Exponential backoff with jitter: `base * 2^attempt`, capped, default
`base=1s, max=5 attempts, cap=5m`. (Note the cap is unreachable at those
defaults — 5 attempts from 1s peaks at 16s. It exists to bound
user-supplied configurations, not the default one.) Two rules:

- **Abort retries if leadership is lost** (SRS FR-307). Continuing to retry
  after demotion means two replicas are working the same fire time.
- Retries are in-memory. A process restart loses pending retries — accepted for
  P1, since durable retry means a queue table and that is River's product.

### 5.4 Idempotency key

Deterministic, so an application can deduplicate downstream even across a
duplicate execution (SRS FR-314):

```
sha256("d-cron:v1:" + namespace + ":" + jobName + ":" + fireTime.UTC().Format(RFC3339))
```

Same fire time on two replicas produces the same key. Available as
`dcron.IdempotencyKey(ctx)`.

---

## 6. Epoch fencing

Phase 3, and the most interesting part of the design.

### 6.1 The problem it solves

Split-brain is unavoidable, not preventable. Concretely: leader A holds the lock
at epoch 7. A's process stalls — a long stop-the-world GC pause, a VM
suspend/migrate, a hypervisor hiccup. Long enough that its TCP connection
drops. PostgreSQL releases the lock. Standby B acquires it, becomes leader at
epoch 8, and starts scheduling. Then A resumes. From A's perspective _nothing
happened_: it still believes it is leader, and it may be mid-way through a job.

We cannot close that window. We can make it harmless: **let stale work be
recognised and rejected.**

### 6.2 Mechanism

An advisory lock gives us no version number, so we need a real monotonic
source. On each successful acquisition, the new leader increments a counter and
reads back its value:

```sql
INSERT INTO dcron.leader_epoch (namespace, epoch, instance_id, acquired_at)
VALUES ($1, 1, $2, now())
ON CONFLICT (namespace) DO UPDATE
  SET epoch       = dcron.leader_epoch.epoch + 1,
      instance_id = EXCLUDED.instance_id,
      acquired_at = now()
RETURNING epoch;
```

Safe under concurrency because only the lock holder ever runs it. Persisting it
means monotonicity survives a full cluster restart (SRS FR-507) — an in-memory
counter would reset to 1 and a long-stalled zombie from the previous cluster
generation could look current again.

The epoch rides in the context, and **every** `d-cron` write is conditioned on
it — including the opening `status = running` insert, not just the terminal
update. SRS FR-310 says _all_ `d-cron`-owned writes, and an earlier draft only
guarded the update, leaving the insert to carry an unvalidated epoch:

```go
ctx = context.WithValue(ctx, epochKey{}, epoch)  // FR-309
```

```sql
-- FR-310, opening write: guarded, not merely stamped
INSERT INTO dcron.execution (namespace, job_name, scheduled_at, started_at,
                             status, instance_id, leader_epoch)
SELECT $1, $2, $3, now(), 'running', $4, $5
WHERE EXISTS (
    SELECT 1 FROM dcron.leader_epoch
    WHERE namespace = $1 AND epoch = $5
);

-- FR-310, terminal write: a demoted leader's write is rejected, not applied
UPDATE dcron.execution SET status = $1, finished_at = now()
WHERE id = $2
  AND leader_epoch = (SELECT epoch FROM dcron.leader_epoch WHERE namespace = $3);
```

Zero rows affected ⇒ we were fenced ⇒ log at `WARN`, increment
`dcron_fenced_writes_total`, discard. That metric going non-zero is a genuine
operational signal worth alerting on.

### 6.3 Fencing user code

Fencing our own bookkeeping is nearly pointless if the user's job already
charged a credit card. So we expose the primitive (SRS FR-311):

```go
// Querier is satisfied by *sql.Tx and by pgx.Tx via a thin adapter,
// so this works for both drivers (SRS NFR-403).
func Fence(ctx context.Context, tx Querier) error  // errors if epoch is stale
```

```go
// usage in a job:
func chargeInvoices(ctx context.Context) error {
    tx, _ := db.BeginTx(ctx, nil)
    defer tx.Rollback()
    if err := dcron.Fence(ctx, tx); err != nil {
        return err  // we are a zombie; commit nothing
    }
    // ... do the work, then tx.Commit()
}
```

**The lock mode here is load-bearing.** A plain `SELECT epoch` inside the user's
transaction has a time-of-check-to-time-of-use hole: under the default READ
COMMITTED isolation, a promotion that commits _after_ our read but _before_ the
user's `tx.Commit()` is invisible to us, and the zombie's charge commits
unfenced. The implementation must take a share lock so the new leader's epoch
`UPDATE` conflicts and one of the two transactions is forced to abort:

```sql
SELECT epoch FROM dcron.leader_epoch WHERE namespace = $1 FOR SHARE;
```

`FOR SHARE` rather than `FOR UPDATE` so that concurrent fenced jobs under the
same epoch don't serialise against each other — only against an actual
leadership change.

This only works for effects that are transactional in the same database. An
HTTP call to Stripe cannot be fenced — for that, the idempotency key (§5.4) and
Stripe's own idempotency support are the answer. The documentation must be
honest about which effects can and cannot be protected.

---

## 7. Missed runs and overlap

Phase 3. Both are policy, both default to the conservative choice.

### 7.1 Missed runs

If no leader existed at 02:00 (database down, all replicas restarting), what
should happen when a leader appears at 02:07?

| Policy                 | Behaviour                                  |
| :--------------------- | :----------------------------------------- |
| `MissedSkip` (default) | Do nothing. Next run is tomorrow at 02:00. |
| `MissedCatchUp`        | Run once, now, for the 02:00 fire time.    |

Default is skip because catch-up is dangerous by surprise: a report emailed 7
hours late may be worse than not sent. Catch-up requires opt-in, a maximum
lookback window, and a cap on how many catch-up runs may be dispatched (SRS
FR-313) — otherwise a database outage over a weekend, with an hourly job, fires
48 executions in one burst on recovery.

Catch-up needs persisted history to know what was missed, so it depends on the
Phase 2 store. Without it, `MissedCatchUp` must be rejected at configuration
time rather than silently doing nothing.

### 7.2 Overlap

Previous run still going when the next fire time arrives:

| Policy                  | Behaviour                                                  |
| :---------------------- | :--------------------------------------------------------- |
| `OverlapSkip` (default) | Skip the new fire, record it as skipped                    |
| `OverlapQueue`          | Run after the current one finishes (bounded queue depth 1) |
| `OverlapAllow`          | Run concurrently                                           |

Skip is the default because it matches what most people actually want from a
"every 5 minutes" job that occasionally takes 7 minutes. Skipped fires must be
_recorded and counted_, not silently dropped — a job quietly skipping half its
runs is exactly the kind of thing that goes unnoticed for months.

---

## 8. Public API

The 10-line integration (SRS NFR-301) is the design constraint that matters
most for adoption:

```go
sched, err := dcron.New(db, dcron.WithSessionStableConnection())
if err != nil { return err }

sched.Add("send-invoices", "0 2 * * *", func(ctx context.Context) error {
    return billing.SendInvoices(ctx)
})

sched.Start(ctx)
defer sched.Stop(ctx)   // bounded by WithDrainTimeout, default 30s
```

The one mandatory option is the session-stability assertion (§3.4). Everything
else has a working default. Requiring that one explicit call is a deliberate
trade of pure zero-config against shipping a foot-gun.

Fuller surface:

```go
package dcron

func New(db *sql.DB, opts ...Option) (*Scheduler, error)
func NewWithPool(pool *pgxpool.Pool, opts ...Option) (*Scheduler, error) // FR-506, NFR-403

// Options — functional; all but one have defaults (FR-601)
func WithSessionStableConnection() Option          // required unless WithDedicatedLockDSN
func WithDedicatedLockDSN(dsn string) Option       // d-cron opens its own direct conn
func WithNamespace(string) Option                  // default "default"
func WithLocation(*time.Location) Option           // default UTC
func WithPollInterval(time.Duration) Option        // default 5s ±20% jitter
func WithDrainTimeout(time.Duration) Option        // FR-315, default 30s
func WithLogger(*slog.Logger) Option
func WithHistory(retention time.Duration) Option   // P2, opt-in schema
func WithSchema(string) Option                     // P2, default "dcron"
func WithSecondsField() Option

// Registration
func (s *Scheduler) Add(name, spec string, fn JobFunc, opts ...JobOption) error
func (s *Scheduler) AddOnce(name string, at time.Time, fn JobFunc, ...) error   // P2, FR-209
func (s *Scheduler) Start(ctx context.Context) error
func (s *Scheduler) Stop(ctx context.Context) error   // drains (bounded), then unlocks

// Per-job options
func WithTimeout(time.Duration) JobOption             // default 30m
func WithRetry(max int, base time.Duration) JobOption // default 5 attempts, base 1s
func WithSinceLastSuccess(time.Duration) JobOption    // P3, FR-210
func WithOverlapPolicy(OverlapPolicy) JobOption       // P3, default OverlapSkip
func WithMissedRunPolicy(MissedRunPolicy) JobOption   // P3, default MissedSkip

// Introspection
func (s *Scheduler) Leadership() LeadershipState       // P2, FR-109
func (s *Scheduler) Jobs() []JobStatus                 // P2
func (s *Scheduler) HealthCheck(ctx) error             // P2, FR-411, k8s probe

// LeadershipState is a three-valued enum, not a bool: FR-109 requires
// leader | standby | unknown, and §3.5's state machine has a real UNKNOWN
// state (before the first attempt, and after a failed liveness probe).
// A bool would collapse "not leader" and "don't know", which is exactly the
// distinction a readiness probe needs.
type LeadershipState int
const (
    LeadershipUnknown LeadershipState = iota
    LeadershipStandby
    LeadershipLeader
)

// Context accessors
func IdempotencyKey(ctx context.Context) string
func Epoch(ctx context.Context) int64                 // P3
func Fence(ctx context.Context, tx Querier) error     // P3, driver-agnostic

type JobFunc func(context.Context) error
```

On the `Stop` example: passing `context.Background()` would make drain
unbounded, so a single stuck 30-minute job hangs `SIGTERM` until the
orchestrator `SIGKILL`s the pod. `WithDrainTimeout` (default 30s, chosen to sit
inside Kubernetes' default 30s grace period) bounds it, and `Stop` cancels
in-flight job contexts when the budget expires.

Two API commitments worth writing down: `JobFunc` takes only a context and
returns only an error — no payloads, no serialisation, because that is the road
to becoming a task queue. And `v0.x` until the epoch design has survived real
production use; we should not promise API stability on a fencing design nobody
has stress-tested yet.

---

## 9. Package layout

```
d-cron/
├── dcron.go              Scheduler, New, Start, Stop
├── job.go                Job, JobFunc, JobOption
├── options.go            Option
├── errors.go             typed errors
├── context.go            epoch / idempotency-key accessors
├── internal/
│   ├── elector/          advisory lock, state machine, pooler detection
│   ├── clock/            min-heap, cron parser, Schedule impls
│   ├── executor/         recover, timeout, retry, overlap
│   └── store/            history, epoch, migrations  (P2+)
├── metrics/              Prometheus  (P2, separate pkg — NFR-402)
├── ui/                   embedded dashboard  (P2, separate pkg)
│   └── assets/           embed.FS, no CDN (FR-407)
├── otel/                 tracing  (P3, separate pkg)
└── examples/
    ├── minimal/
    ├── with-dashboard/
    └── kubernetes/       5-replica manifest for AC-01..AC-03
```

`metrics`, `ui`, and `otel` are separate packages specifically so an app that
doesn't use them doesn't link `prometheus/client_golang` or the OTel SDK. This
is a real binary-size and dependency-audit concern for the "no new
infrastructure" pitch, and it's easy to get wrong by putting a Prometheus
counter in the core executor.

---

## 10. Database schema

**Phase 1: nothing.** No tables, no migrations, no `CREATE` of any kind
(SRS FR-501, AC-09). This is a selling point and we should not erode it.

**Phase 2+, opt-in, in schema `dcron` by default** (never `public` — we should
not litter a user's default schema):

```sql
CREATE SCHEMA IF NOT EXISTS dcron;

-- P3
CREATE TABLE IF NOT EXISTS dcron.leader_epoch (
    namespace   text PRIMARY KEY,
    epoch       bigint      NOT NULL,
    instance_id text        NOT NULL,
    acquired_at timestamptz NOT NULL DEFAULT now()
);

-- P2
CREATE TABLE IF NOT EXISTS dcron.execution (
    id            bigserial PRIMARY KEY,
    namespace     text        NOT NULL,
    job_name      text        NOT NULL,
    scheduled_at  timestamptz NOT NULL,
    started_at    timestamptz NOT NULL,
    finished_at   timestamptz,
    status        text        NOT NULL,   -- running|success|failed|panicked|skipped|timeout
    attempt       int         NOT NULL DEFAULT 1,
    duration_ms   bigint,
    error         text,
    instance_id   text        NOT NULL,
    leader_epoch  bigint      NOT NULL    -- P3, fencing
);

CREATE INDEX IF NOT EXISTS execution_job_time_idx
    ON dcron.execution (namespace, job_name, scheduled_at DESC);
```

Migrations are `IF NOT EXISTS` DDL wrapped in a transaction, guarded by a
_separate_ advisory lock so that N replicas starting simultaneously don't race
(SRS FR-504). No migration framework — the schema is two tables and forcing
`golang-migrate` on users would contradict the whole premise.

Retention pruning runs on the leader as an internal `d-cron` job.

---

## 11. Observability

**Logs** via `log/slog` only (SRS FR-401) — we accept a `*slog.Logger` and
never impose a framework. Every leadership transition logs at `INFO` with
instance ID and epoch; these lines are what someone reads at 3am.

**Metrics** (P2), in the `metrics` subpackage, registered into a
caller-supplied registry (FR-404):

| Metric                                   | Type      | Notes                                                                      |
| :--------------------------------------- | :-------- | :------------------------------------------------------------------------- |
| `dcron_is_leader`                        | gauge     | 1/0 — see the alerting note below; the naive "always 1" invariant is wrong |
| `dcron_leader_transitions_total`         | counter   | flapping detector                                                          |
| `dcron_job_executions_total{job,status}` | counter   |                                                                            |
| `dcron_job_duration_seconds{job}`        | histogram |                                                                            |
| `dcron_job_last_success_timestamp{job}`  | gauge     | best staleness alert                                                       |
| `dcron_jobs_running{job}`                | gauge     |                                                                            |
| `dcron_fenced_writes_total`              | counter   | **non-zero means split-brain occurred**                                    |
| `dcron_missed_runs_total{job}`           | counter   |                                                                            |

Two of these are the reason to build the whole feature: they are alerts nobody
can currently write. But the leader-count alert needs a duration qualifier —
`sum(dcron_is_leader) != 1` is legitimately 0 for a poll interval on **every**
normal failover, 0 while the database is unreachable (§12 row 5), and 2 during
the split-brain window (§12 row 4). Alerting on the instantaneous value pages on
healthy failover and on every DB blip. Ship these as the documented rules:

```promql
# No leader for longer than a few poll intervals — jobs are not running
sum(dcron_is_leader) == 0
  for: 2m

# Two leaders at once, sustained — split-brain beyond the expected window
sum(dcron_is_leader) > 1
  for: 30s

# A demoted leader tried to write. Any occurrence is worth knowing about.
increase(dcron_fenced_writes_total[5m]) > 0
```

**Dashboard** (P2) — a plain `http.Handler` the app mounts wherever it likes,
server-rendered `html/template`, assets via `embed.FS`, no CDN, no build step,
no JS framework. Read-only in P2. Off by default, and the docs must say
clearly that authentication is the host app's job (FR-408) — we should not ship
an unauthenticated admin surface and hope.

---

## 12. Failure modes

Walked through explicitly, because this table _is_ the product:

|  #  | Scenario                                 | What happens                                                                                         | Result                                                                                                                                                               |
| :-: | :--------------------------------------- | :--------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
|  1  | Leader exits cleanly                     | `pg_advisory_unlock`, drain (bounded), close                                                         | Standby promoted in ~1 poll                                                                                                                                          |
|  2  | Leader `SIGKILL`ed, **host alive**       | Kernel sends FIN/RST, PG reaps backend                                                               | Standby promoted in ~1 poll                                                                                                                                          |
|  3  | **Leader host dies / loses power**       | Backend blocks in `recv()`, learns nothing                                                           | ⚠️ **Lock held until keepalives expire — hours at PG defaults. No promotion, nothing runs.** Requires operator config (§3.1, SRS C-06)                               |
|  4  | **Leader partitioned from DB**           | Leader self-demotes on liveness failure, but its backend is still alive server-side holding the lock | ⚠️ **No replica can be promoted** until keepalives expire. An earlier draft claimed prompt promotion here — false                                                    |
|  5  | Leader stalls, conn survives             | Retains lock                                                                                         | Runs are **late, not duplicated**                                                                                                                                    |
|  6  | Leader stalls, conn drops                | B promoted at epoch+1; A resumes as zombie                                                           | **Split-brain window** — epoch fencing rejects A's same-database writes; other effects need idempotency keys                                                         |
|  7  | DB unreachable, all replicas             | No leader, no executions                                                                             | Resumes per missed-run policy (default: skip)                                                                                                                        |
|  8  | Behind PgBouncer `transaction` mode      | Not detectable at runtime (§3.4)                                                                     | **Refuses to start** unless operator asserts session stability or supplies a dedicated lock DSN (FR-108). If misconfigured anyway: duplicate leaders + orphaned lock |
|  9  | Pool `MaxOpenConns(1)`                   | Startup check on `db.Stats()`                                                                        | Refuses to start (FR-112)                                                                                                                                            |
| 10  | Namespace collision between two apps     | Both fight for one lock; one app never schedules                                                     | Docs + UI display resolved key                                                                                                                                       |
| 11  | Rolling deploy, mixed job sets           | Leadership may land on either version                                                                | Tolerated, not coordinated (SRS A-02)                                                                                                                                |
| 12  | Job ignores its context                  | Runs past timeout                                                                                    | Logged; cannot be killed — documented limitation                                                                                                                     |
| 13  | **Job panics on a goroutine it spawned** | Not recoverable by our `defer`                                                                       | ⚠️ **Process dies.** Only panics on the job's own goroutine are contained (SRS FR-301)                                                                               |
| 14  | Clock skew between replicas              | Only leader's clock consulted                                                                        | Fire times drift; **no double fire**                                                                                                                                 |

Rows 3, 4, 5, 6, 8, 12, 13 and 14 are the ones we must be loudest about in the
README. Rows 3, 4 and 13 in particular were understated or wrong in an earlier
draft — overselling any of them is how this project loses credibility, and the
failure-mode table is the first thing a serious evaluator reads.

---

## 13. Testing strategy

The whole value proposition is correctness under failure, so tests are the
deliverable as much as the library is.

**Unit** — cron parsing (table-driven, including DST boundaries and leap
years), heap ordering, backoff computation, epoch comparison. No database.

**Integration**, real PostgreSQL via `testcontainers-go`, matrix over PG 12 and
current stable (AC-10):

- N schedulers in one process, one becomes leader, N-1 do not
- kill leader's connection ⇒ exactly one standby promotes
- epoch increments monotonically across promotions, and survives full restart
- a write stamped with a stale epoch affects zero rows — **both** the opening
  insert and the terminal update
- `Fence()` under concurrent promotion: the zombie's transaction must abort, not
  commit (the `FOR SHARE` behaviour in §6.3)
- re-`try_lock` on a held key returns true and a single unlock does not release
  it — **a regression test for C-07**, so nobody reimplements the liveness check
  as a re-acquire
- refuses to start without a session-stability assertion, and with
  `MaxOpenConns(1)`
- **against a real PgBouncer in `transaction` mode**: document the two hazards
  (orphaned lock survives client disconnect; two clients acquire the same key)
  and assert that the `pg_backend_pid()` probe does _not_ distinguish it — a
  regression test against reintroducing probe-based detection
- with keepalives disabled, a partitioned leader's lock is **not** released
  promptly (asserting the limitation, per SRS AC-02b)

**Chaos / soak** — the acceptance criteria as executable tests. 5 replicas in
Kubernetes (or 5 processes), a job firing every minute, an external ledger
counting executions.

Assert **exactly one execution per minute** across: steady state, leader
`SIGKILL` on a live host, rolling restart, and database restart.

Do **not** assert exactly-one under induced `SIGSTOP` pauses. An earlier draft
listed that scenario alongside the others, which would have encoded the very
exactly-once claim SRS §6.2 forbids — failure mode 6 says plainly that this case
can duplicate. The `SIGSTOP` test is still worth running, but its assertions are
different and weaker:

- at most one _committed_ `d-cron` execution record per fire time
- `dcron_fenced_writes_total` increments (proving fencing engaged, not that
  duplication was prevented)
- the zombie's `Fence()`d transaction aborted
- duplicate _invocations_ may occur and the test records them as expected
  behaviour, not failure

The soak test is the artifact that substantiates our claims — so it must
substantiate the claims we actually make (at-most-once normally, fenced under
split-brain) and not one we have explicitly disowned.

**Race and fuzz** — `-race` on everything in CI; fuzz the cron parser.

It should run nightly and its results should be published in the README.

---

## 14. Decisions and rejected alternatives

| Decision              | Chosen                                                      | Rejected                                        | Why                                                                                                                                                                                                               |
| :-------------------- | :---------------------------------------------------------- | :---------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Coordination strategy | Leader election                                             | Per-job lock racing                             | O(1) coordination instead of O(jobs × replicas) round-trips at every trigger boundary. Lock racing spikes DB CPU and connections at :00 of every minute — the flaw in `gocron`'s locker approach.                 |
|                       |                                                             | Consistent hash ring (`libi/dcron`)             | Needs membership tracking, which needs Redis/etcd. Rebalancing on scale events is subtle. Contradicts "no new infrastructure".                                                                                    |
|                       |                                                             | Raft between replicas (`hashicorp/raft`)        | Needs stable peer addresses; fights with ephemeral container IPs and autoscaling. Large complexity for a problem one lock solves.                                                                                 |
|                       |                                                             | Dedicated single scheduler pod (asynq's advice) | SPOF, second deployment, split mental model. This is the thing we exist to remove.                                                                                                                                |
| Lock primitive        | Session-level `pg_try_advisory_lock`                        | Transaction-level (`_xact_`)                    | Would need an open transaction for the whole leadership term — holds locks and bloats WAL.                                                                                                                        |
|                       |                                                             | Row lock + TTL heartbeat (River's approach)     | Needs a table (breaks FR-501) and hand-written lease renewal. Advisory locks give liveness free.                                                                                                                  |
| Failure detection     | PostgreSQL session reaping **plus mandated TCP keepalives** | Session reaping alone                           | Reaping alone covers only process death on a live host; host death leaves the lock held for hours at PG defaults (§3.1). Keepalives are a deployment requirement, not a tuning tip.                               |
|                       |                                                             | Application-level heartbeat table               | Less code and fewer bugs for the process-death case, and it needs no table (FR-501). We accept that host-death detection latency is then governed by keepalive config rather than by us.                          |
| Pooler safety         | Operator assertion / dedicated lock DSN                     | Runtime probe (`pg_backend_pid()` twice)        | Probe returns identical PIDs through PgBouncer `transaction` mode at startup — a false negative exactly when it matters (§3.4). A detection scheme that silently passes in the dangerous case is worse than none. |
| Liveness check        | `SELECT 1` + optional `pg_locks` lookup                     | Re-`pg_try_advisory_lock`                       | Advisory locks are re-entrant; re-acquiring every interval breaks the single unlock on shutdown (C-07).                                                                                                           |
| Fencing source        | Persisted counter table                                     | In-memory counter                               | Resets to 1 on full restart; a stale zombie could appear current.                                                                                                                                                 |
|                       |                                                             | `pg_current_xact_id()` / LSN                    | Monotonic but opaque, and couples us to PG internals.                                                                                                                                                             |
| Job signature         | `func(context.Context) error`                               | Payload + serialisation                         | That's a task queue. Stay out of River's lane.                                                                                                                                                                    |
| Retry durability      | In-memory                                                   | Persisted retry queue                           | Requires a queue table. Explicitly out of scope (SRS §7).                                                                                                                                                         |
| Timezone              | One per scheduler                                           | Per job                                         | Mixed-timezone DST transitions produce confusing double/skipped fires.                                                                                                                                            |
| Dashboard             | Server-rendered, `embed.FS`                                 | SPA + REST                                      | No build step, no CDN, works air-gapped, ~zero dependencies.                                                                                                                                                      |
| Default DB schema     | `dcron`                                                     | `public`                                        | Don't pollute the user's default schema.                                                                                                                                                                          |

---

## 15. Open questions

| ID  | Question                                                                                                             | Leaning                                                                                                                                                                                                                                                    |
| :-- | :------------------------------------------------------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| O-1 | Write our own cron parser, or vendor `robfig/cron`'s?                                                                | Write it. ~200 lines, keeps the core dependency-free (NFR-401), and the spec is well documented. Revisit if DST handling gets hairy.                                                                                                                       |
| O-2 | Should the leader also verify lock ownership via `pg_locks`, not just `SELECT 1`?                                    | **Lean yes, and move to P1.** A `SELECT 1` proves the connection is alive but not that we still hold the key — and it must not be a re-`try_lock` (C-07), so `pg_locks` filtered by `objid` and `pg_backend_pid()` is the only safe positive confirmation. |
| O-9 | Can we shorten host-death detection without keepalives, e.g. a leader-written liveness timestamp that standbys read? | Possibly, but it needs a table and so breaks FR-501 for P1. Worth revisiting in P2 once the store exists — it would make failover independent of operator keepalive config, which is the weakest link in the current design.                               |
| O-3 | Expose a `LeadershipChanged` callback for app code?                                                                  | Yes, small and clearly useful (cache warming, feature gating).                                                                                                                                                                                             |
| O-4 | ~~Support `pgx` natively or only `database/sql`?~~                                                                   | **Closed.** NFR-403 and FR-506 are both P1, so a P1 requirement cannot rest on an open question. Resolved in §8: `New` for `*sql.DB`, `NewWithPool` for `*pgxpool.Pool`, internal `Querier`/`conn` interfaces, `Fence` accepts both.                       |
| O-5 | Is `MissedCatchUp` worth building at all, given the risk?                                                            | Build it, off by default, with hard caps. Users will ask for it; better we ship a safe version than they hand-roll one.                                                                                                                                    |
| O-6 | Warn on suspected namespace collision by writing an advisory row?                                                    | Would need a table in P1. Probably just document loudly + surface in UI.                                                                                                                                                                                   |
| O-7 | License — Apache-2.0 or MIT?                                                                                         | Apache-2.0, for the patent grant.                                                                                                                                                                                                                          |
| O-8 | Name — is `d-cron` too close to `libi/dcron`?                                                                        | **Likely needs changing.** Direct collision with an existing 464-star project is a real problem for discoverability and goodwill. Worth resolving before the first public commit.                                                                          |

---

## 16. Build order

Ship P1 as a genuinely finished thing before starting P2. A half-built
dashboard on top of unproven election logic helps nobody.

**Phase 1**

1. `internal/elector` — lock, state machine, session-stability gate (§3.4),
   pool-size check, keepalive warning, `pg_locks` liveness (never re-acquire)
2. `internal/clock` — parser, heap, `cronSchedule` + `intervalSchedule`
3. `internal/executor` — recover, timeout, retry, bounded drain
4. `Scheduler` wiring, both constructors (`*sql.DB` and `pgxpool.Pool`),
   options, `slog`
5. Integration tests including the C-07 and PgBouncer regression tests, then the
   5-replica soak test → validate AC-01..AC-10
6. README leading with the correctness model and failure-mode table (§12),
   required keepalive settings, and the migration guide from `robfig/cron`

**Phase 2** — `internal/store` + idempotent migrations, `onceSchedule`
(FR-209), `metrics`, `ui`, health check, failure webhook.

**Phase 3** — `leader_epoch` table, epoch in context, fenced writes (insert
_and_ update), `Fence()` with `FOR SHARE`, `sinceSuccessSchedule` (FR-210),
overlap policy, missed-run catch-up with caps, `otel`.

**Phase 4** — extract `Coordinator` interface, alternative backends, runtime
job management, admin API.

Before any code, one gate: resolve [O-8](#15-open-questions) — the name collides
with `libi/dcron`, and that is worth settling before the first public commit.

The competitive analysis is **already complete** and is recorded in SRS
Appendix A, verified 2026-08-13 against primary sources — including
confirmation that `gocron` v2 does ship a `DistributedElector`, which an
earlier internal draft denied. An earlier version of this section asked for that
verification as a pre-code task; it has since been done, and Appendix A's
"claims that must not be made" list is the load-bearing output. Do not redo it;
do read it before writing any comparison in the README.

---

_Comments and disagreement welcome on the tracking issue._
