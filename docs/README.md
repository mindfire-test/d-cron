# `d-cron` - Distributed Cron for Horizontally-Scaled Go Applications

**`d-cron`** is a lightweight, embeddable Go library that provides safe, coordinated cron-style job scheduling across an arbitrary number of application replicas - **using only the PostgreSQL database your application already operates.**

---

## 1. The Problem & Positioning

### The Problem

Standard in-process cron libraries maintain an in-memory timer heap scoped to a single process. When your application scales horizontally to `N` container replicas, each replica independently reaches the trigger time and executes the job. A job scheduled for `0 2 * * *` (2:00 AM) therefore **fires `N` times simultaneously**.

### Product Positioning

`d-cron` occupies the unoccupied space between a simple in-process cron library and a heavy workflow engine:

```
robfig/cron ────► d-cron ────────► River / asynq ──► Dkron ──────► Temporal
in-process        coordinated      task queue        standalone    workflow
cron only         cron (this)      + cron            cluster       engine
```

- **Zero New Infrastructure**: No Redis, no etcd, no sidecars. Uses PostgreSQL session-bound advisory locks (`pg_try_advisory_lock`).
- **Single Leader Scheduler**: 1 active Leader replica runs the timer clock; 0 thundering-herd database lock spikes at trigger boundaries.
- **In-Process Go Functions**: Register ordinary Go functions (`Add`). No queue tables, no schema migrations, no JSON argument serialization required for Phase 1.

---

## 2. Feature Comparison

| Factor                  | `robfig/cron` | `gocron`          | `asynq`       | `river`           | **`d-cron`**                   |
| :---------------------- | :------------ | :---------------- | :------------ | :---------------- | :----------------------------- |
| **Architecture**        | In-Process    | In-Process        | Task Queue    | Task Queue        | **In-Process**                 |
| **Infra Required**      | None          | Redis             | Redis         | Postgres + Tables | **Postgres (No extra tables)** |
| **Coordination**        | None          | Per-Job Lock Race | Queue Enqueue | Leader Election   | **Leader Election**            |
| **Leader Election**     | ✗ No          | ✗ No              | ✗ No          | ✓ Yes             | **✓ Yes**                      |
| **Split-Brain Fencing** | ✗ No          | ✗ No              | ✗ No          | ✗ No              | **✓ Yes**                      |
| **License**             | Open Source   | Open Source       | Open Source   | Paid              | **Open Source**                |

---

## 3. Quickstart

### Installation

```sh
go get github.com/mindfire-test/d-cron
```

### Usage Example

```go
import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"time"

	"github.com/mindfire-test/d-cron"
	_ "github.com/lib/pq" // register the Postgres driver
)

func main() {
	db, err := sql.Open("postgres", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize d-cron. WithNamespace scopes the lock. The session-stability
	// assertion (SDS §3.4, issue #12) is mandatory: a transaction-mode pooler
	// corrupts advisory-lock semantics, so d-cron refuses to start without
	// WithSessionStableConnection() or a dedicated lock connection.
	scheduler, err := dcron.New(db,
		dcron.WithNamespace("billing"),
		dcron.WithPollInterval(3*time.Second),
		dcron.WithSessionStableConnection(),
		dcron.WithLogger(slog.Default()),
	)
	if err != nil {
		log.Fatalf("Failed to initialize d-cron: %v", err)
	}

	// Register an in-process Go job (cron spec syntax)
	err = scheduler.Add("daily-cleanup", "0 2 * * *", func(ctx context.Context) error {
		slog.Info("Executing daily report cleanup",
			slog.String("job", "daily-cleanup"),
			slog.Int64("epoch", dcron.Epoch(ctx)),
			slog.String("idempotency_key", dcron.IdempotencyKey(ctx)),
		)
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to register job: %v", err)
	}

	// Start leader election & the scheduler loop.
	rootCtx := context.Background()
	if err := scheduler.Start(rootCtx); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}
	// Stop must be BOUNDED (issue #22): passing context.Background() here would
	// let a single stuck 30-minute job hang SIGTERM until the orchestrator
	// SIGKILLs the pod. Give it the same 30s budget as WithDrainTimeout.
	stopCtx, stopCancel := context.WithTimeout(rootCtx, 30*time.Second)
	defer func() {
		stopCancel()
		if err := scheduler.Stop(stopCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
}
```

---

## 4. Phase 2 — Observability

Phase 2 adds opt-in observability without touching the Phase-1 guarantee that
the core path creates **zero tables** (AC-09). Everything below is off unless
you turn it on.

### Leadership, health, and introspection

```go
// Three-valued leadership state — NOT a bool: "not leader" and "don't know"
// are different answers, and a readiness probe needs to tell them apart (FR-109).
switch sched.Leadership() {
case dcron.LeadershipLeader:  // this replica holds the lock and runs the clock
case dcron.LeadershipStandby: // polling for promotion
case dcron.LeadershipUnknown: // before the first poll / after a DB error
}

// Kubernetes probes (FR-411): see examples/kubernetes for /healthz and /readyz.
if err := sched.HealthCheck(ctx); err != nil { /* backend unreachable */ }

// Point-in-time job snapshot for dashboards and diagnostics (FR-406).
for _, j := range sched.Jobs() {
    fmt.Println(j.Name, j.Spec, j.NextRun, j.LastOutcome, j.LastDurationMS)
}
```

### One-off jobs

```go
// Fires exactly once at `at`, then is evicted from the heap (issue #33).
// Not persisted: re-register it on process restart.
_ = sched.AddOnce("warm-cache", time.Now().Add(30*time.Second), func(ctx context.Context) error {
    return warmCache(ctx)
})
```

### Failure notification hooks

```go
sched, err := dcron.New(db,
    dcron.WithSessionStableConnection(),
    dcron.WithHooks(&dcron.WebhookHook{
        URL:     "https://alerts.example.com/dcron",
        Timeout: 5 * time.Second,
        Headers: map[string]string{"Authorization": "Bearer ..."},
    }),
    dcron.WithHooks(dcron.HookFunc(func(ctx context.Context, res executor.Result) error {
        log.Printf("job %s finished: %s after %d attempt(s)", res.Name, res.Outcome, res.Attempts)
        return nil
    })),
)
```

Hooks fire asynchronously after each execution completes; a hook error is
logged and never fails the job or the scheduling loop.

### Metrics (opt-in adapter)

The core never links a metrics SDK (NFR-402) — enforced by
`metrics.TestCoreDoesNotLinkMetricsSDK`. You supply a
`metrics.Recorder` via `dcron.WithMetrics(rec)`; bridge it to your own
registry using the exported metric keys:

| Key | Type | Notes |
| :-- | :--- | :---- |
| `dcron_is_leader` | gauge | 1/0 |
| `dcron_leader_transitions_total` | counter | flapping detector |
| `dcron_job_executions_total{job,status}` | counter | status: success/failed/panicked/timeout/canceled/skipped |
| `dcron_job_duration_seconds{job}` | histogram | per logical execution incl. retries |
| `dcron_job_last_success_timestamp{job}` | gauge | best staleness alert |
| `dcron_jobs_running{job}` | gauge | concurrency |
| `dcron_fenced_writes_total` | counter | non-zero means split-brain occurred |
| `dcron_missed_runs_total{job}` | counter | skipped/catch-up fires |

**Alert with duration qualifiers, never the instantaneous value** —
`sum(dcron_is_leader)` is legitimately 0 during every normal failover and 2
during the split-brain window:

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

### Execution history (opt-in schema)

`dcron.WithHistory(retention)` enables durable execution history in schema
`dcron` (**never** `public`; configurable via `dcron.WithSchema`). This is the
one feature that creates tables — the "zero migrations" claim is Phase-1 only:

- Migration is idempotent (`IF NOT EXISTS`) DDL wrapped in a transaction,
  guarded by a **separate advisory lock** so N replicas starting at once do not
  race (FR-504). It is safe to call from every replica at startup.
- Every execution writes one row with status
  `running|success|failed|panicked|skipped|timeout`, indexed on
  `(namespace, job_name, scheduled_at DESC)`.
- Retention pruning runs on the leader as an internal job; history write
  failures are logged and never fail the job itself.
- All queries are parameterised; the schema identifier is validated against an
  allowlist (NFR-503).

### Embedded dashboard (opt-in mounting)

```go
mux.Handle("/internal/dcron", ui.Handler(
    func() ui.Overview {
        return ui.Overview{
            Namespace:      sched.Namespace(),
            InstanceID:     sched.InstanceID(),
            LockKey:        sched.Key(),   // namespace collisions show up here
            Leadership:     sched.Leadership().String(),
            HistoryEnabled: true,
            Schema:         "dcron",
            Jobs:           nil, // fill from sched.Jobs()
        }
    },
    nil, // optional: recent history rows from the store
))
```

Server-rendered `html/template`, assets embedded via `embed.FS`, no CDN, no
build step, works air-gapped (FR-407). Read-only in Phase 2 (NFR-502). It
performs **no authentication**: mounting it behind your own auth middleware is
your responsibility (FR-408).

---

## 5. Correctness Model & Guarantees

### What `d-cron` Guarantees

- **Under Normal Operation**: At-most-once execution per scheduled fire time across all replicas.
- **Under Leader Failover**: Automatic standby promotion within 1 poll interval on process exit.
- **Split-Brain Protection**: Monotonic leader epoch tokens (`LeaderEpoch`) injected into job `context.Context` to fence stale database writes.
- **Single Replica Parity**: If deployed with `N=1`, degrades gracefully to behave as an ordinary in-process cron.

### Failure Mode Behaviour

| Failure Scenario       | System Behaviour                                                                                                                                                           |
| :--------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Graceful Stop**      | Advisory lock explicitly released via `pg_advisory_unlock`; standby promoted within 1 poll interval.                                                                       |
| **Process SIGKILL**    | Kernel closes TCP socket; PostgreSQL reaps backend session and frees lock; standby promoted within 1 poll interval.                                                        |
| **Network Cut**        | PostgreSQL backend blocks in `recv()`. **Lock is NOT released until TCP Keepalives expire** (requires operator configuration below).                                       |
| **Transaction Pooler** | Transaction-mode poolers hand 1 server session to multiple clients, breaking lock semantics. Requires operator assertion (`WithSessionStableConnection`) or dedicated DSN. |

---

## 6. Mandatory Operator Configuration

### PostgreSQL TCP Keepalives

PostgreSQL releases session advisory locks when the backend process exits. If a host physically loses power or gets partitioned, PostgreSQL's default TCP keepalives on Linux will delay failover for hours.

Operators **MUST** configure DSN keepalive settings for prompt host-death failover:

```dsn
postgres://user:pass@host:5432/db?keepalives=1&keepalives_idle=5&keepalives_interval=2&keepalives_count=3
```

### PgBouncer / Connection Poolers

Session-level advisory locks are bound to a database session. Transaction-level poolers (like PgBouncer in `transaction` mode) recycle sessions across clients, which can cause orphaned locks or duplicate leader acquisition.

`d-cron` requires operators to pass an explicit assertion option or use a dedicated direct DSN:

```go
// Session-stability options are Phase 1 (SDS §3.4) and not yet on the v0.x scaffolding.
dcron.WithSessionStableConnection()      // Operator asserts direct connection or session-mode pooler
dcron.WithDedicatedLockDSN(dsn)          // DCron opens its own dedicated direct connection
```

---

## 7. Development & Workflow

### Required Tooling

| Tool          | Version            | Verification            |
| :------------ | :----------------- | :---------------------- |
| Go            | `go 1.23` (go.mod) | `go version`            |
| gofumpt       | `v0.10.0`          | `gofumpt -version`      |
| golangci-lint | `v1.64.8`          | `golangci-lint version` |

### Makefile Commands

```sh
make fmt        # Format code with gofumpt
make check      # Verify formatting compliance
make vet        # Run go vet ./...
make lint       # Run golangci-lint
make test       # Run unit and integration tests
make build      # Build all packages
make ci         # Run complete CI gate locally (fmt -> vet -> lint -> build -> test)
```

### Commit Message Standard

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>[(<scope>)]: <subject>
```

_Example_: `feat(elector): add postgres advisory lock acquirer`

### Branching Model

One branch **per feature**, cut from the latest `development` — never a
phase-wide branch bundling unrelated work. Branch names mirror the
conventional-commit type/scope:

```
<type>/<feature-scope>        e.g. feat/pgx-support, test/integration-testcontainers,
                                   docs/godoc-sweep, fix/history-write-path
```

Rules of thumb:

- `feat/<pkg-or-capability>` — new user-facing capability (one issue or one
  cohesive package; if it spans two independent issues, that is two branches).
- `fix/<area>` — defect fix.
- `test/<what-is-tested>` — test-only harnesses and suites (e.g. the
  testcontainers integration suite).
- `docs/<what-is-documented>` — documentation-only changes.

Work flow: `git checkout development && git pull` → `git switch -c
<type>/<scope>` → commit (hooks run fmt/lint/build + conventional-commit) →
push → PR back into `development`. Keep the branch scoped: if a PR's diff
spans features, split it.

---
