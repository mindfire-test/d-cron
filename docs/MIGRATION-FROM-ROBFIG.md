# Migrating from `robfig/cron` to d-cron

This guide maps the `github.com/robfig/cron/v3` API you know onto d-cron's
API, and calls out every behavioural difference we could find so nothing
surprises you at 3am (issue #31, NFR-302).

The 60-second version: **d-cron is not a drop-in replacement.** It is
`robfig/cron` plus distributed leadership — only one replica fires each job —
which is exactly why several knobs work differently.

---

## 1. Before / after

**Before (`robfig/cron/v3`):**

```go
c := cron.New(cron.WithLocation(loc), cron.WithChain(cron.Recover(cron.DefaultLogger)))
c.AddFunc("*/5 * * * *", func() { sendInvoices() })
c.Start()
defer c.Stop()
```

Every replica of your deployment runs this — so **every** replica fires the
job. If you deduplicated downstream, that was your job.

**After (d-cron):**

```go
db, _ := sql.Open("postgres", os.Getenv("DATABASE_URL"))

sched, err := dcron.New(db,
	dcron.WithNamespace("billing"),          // isolates lock keys per app
	dcron.WithLocation(loc),
	dcron.WithSessionStableConnection(),     // or WithDedicatedLockDSN(...)
)
if err != nil {
	log.Fatal(err) // refuses to start on unsafe pooler configs — see below
}

_ = sched.Add("send-invoices", "*/5 * * * *", func(ctx context.Context) error {
	sendInvoices(ctx)      // respect ctx: shutdown cancels it
	return nil
})

if err := sched.Start(context.Background()); err != nil { /* ... */ }

// On SIGINT/SIGTERM:
drain, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
_ = sched.Stop(drain) // unlock -> drain in-flight -> close
```

One line changes everything: jobs registered with `Add` fire on exactly one
replica (the elected leader); the others idle as standbys and take over if it
dies.

---

## 2. Option mapping

| robfig/cron | d-cron | Notes |
| :---------- | :----- | :---- |
| `cron.New()` | `dcron.New(db, opts...)` | d-cron needs a `*sql.DB`; see session stability below |
| `cron.WithLocation(loc)` | `dcron.WithLocation(loc)` | Same semantics; default UTC in both |
| `cron.WithSeconds()` parser option | `dcron.WithSecondsField()` | Opt-in 6-field specs; default is 5-field in both libraries |
| `cron.WithChain(cron.Recover(...))` | built-in | Panics are recovered per attempt, reported as outcome `panicked`, and retried like any failure |
| `cron.WithChain(cron.DelayIfStillRunning)` | `dcron.WithNoOverlap()` | Per-job option; suppresses a fire while the previous run is still active (both libraries default to allowing overlap) |
| custom retry wrappers | `dcron.WithRetry(dcron.Retry{...})` | Exponential backoff + jitter built in; bounded by job context |
| one-off schedules / custom `Schedule` | `sched.AddOnce(name, when, fn)` | First-class single fire; heap evicts after it runs |
| `c.AddFunc(spec, fn)` | `sched.Add(name, spec, fn)` | Jobs are **named**; names feed metrics, history, hooks, and idempotency keys |
| `c.AddJob` / typed `Job` interface | same `func(ctx context.Context) error` signature | The context carries epoch + idempotency key accessors |
| `c.Entry(id)` / inspection | `sched.Jobs()` | Snapshot incl. next run, last outcome/duration/error |
| `c.Stop()` | `sched.Stop(ctx)` | ctx bounds the drain of in-flight jobs (default 30s via `WithDrainTimeout`) |

Not portable (intentionally): custom parser pipelines (`cron.NewParser`,
`ParseStandard` returning raw entries) — d-cron owns parsing so it can
guarantee monotonic fire times across replicas.

---

---

## 3. Behavioural differences — read before migrating

1. **Missed fires produce zero executions (by design).** If a replica is down
   past a fire time, that fire is skipped and logged (`dcron_missed_runs_total`
   rises). robfig behaves the same way while running, but d-cron documents it
   as a correctness guarantee: catch-up execution is opt-in in a later phase.
   **Never assume lost time is replayed.**
2. **Correctness model: at-most-once normally, at-least-once under failure
   with future catch-up — never "exactly-once".** A demoted leader can
   theoretically complete an in-flight run concurrently with the new leader
   (the goroutine cannot be killed). Make jobs idempotent; d-cron hands each
   fire a deterministic idempotency key:
   `dcron.IdempotencyKey(ctx)` (sha256 of namespace/job/fire-time).
3. **Jobs get contexts.** Long loops must check `ctx.Done()`: shutdown and
   demotion cancel them. A job that ignores its context outlives its term.
4. **Startup gates can refuse to run** (robfig has none):
   - No session-stability assertion → `ErrSessionStabilityUnasserted`.
   - Pool with `MaxOpenConns == 1` borrowed for the lock → typed error.
   Use a direct connection or `WithDedicatedLockDSN` behind PgBouncer.
5. **Panics do not crash the process.** robfig's Recover wrapper logs and
   moves on; d-cron additionally records the panic (with stack) as the job's
   outcome and applies retry policy to it.
6. **Timezones resolve identically**, but d-cron guarantees strictly
   monotonic `Next` values even around DST gaps — a schedule that would land
   inside a spring-forward gap resolves to the next valid wall-clock minute.

---

## 4. Make your jobs idempotent

Under leader failover the same fire time can be attempted twice (old leader
finishing while the new one starts). Treat every job body as re-runnable:

```go
key := dcron.IdempotencyKey(ctx) // stable across replicas for the same fire
// pass `key` to your payment/email API as its idempotency token, or guard
// with INSERT ... ON CONFLICT DO NOTHING keyed by `key`.
```

---

## 5. Quickstart checklist

- [ ] Direct DB connection (or `WithDedicatedLockDSN`) for the advisory lock
- [ ] TCP keepalives enabled server-side — required, not optional (see README
      §"PostgreSQL TCP Keepalives"); without them a partitioned leader holds
      the lock for hours
- [ ] Namespace chosen per application (`WithNamespace`)
- [ ] Every `Add` call checked for error (dupes/specs are programmer errors)
- [ ] Signal handler calling `Stop(drainCtx)` with a sane drain budget
