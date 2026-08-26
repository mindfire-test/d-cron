# pgx support in d-cron

Status, guidance, and the supported integration path for
[`github.com/jackc/pgx`](https://github.com/jackc/pgx) users (issues #24/#27,
FR-506, NFR-403).

## TL;DR

d-cron's core is `database/sql`-based and stays **driver-agnostic with zero
third-party dependencies** (NFR-401). pgx works today through its official
`stdlib` adapter — no core changes required. A native `NewWithPool(*pgxpool.Pool)`
constructor is deliberately **deferred** (see "Why not yet" below); the
pluggable `dcron.LockBackend` seam is the long-term home for a native pgx
backend.

## Using pgx v5 with d-cron today

```go
import (
	"context"
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib" // registers driver name "pgx"
	dcron "github.com/mindfire-test/d-cron/dcron"
)

func main() {
	sqlDB, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil { /* ... */ }
	defer sqlDB.Close()

	sched, err := dcron.New(sqlDB,
		dcron.WithNamespace("billing"),
		// pgx pools are connection pools; assert session stability only for a
		// direct connection or session-mode pooler. Safer: dedicated lock DSN.
		dcron.WithSessionStableConnection(),
	)
	// ... Add / Start / Stop as usual.
}
```

**Recommended behind PgBouncer:** let d-cron own one direct connection for the
advisory lock, bypassing any pooler. With pgx, name the driver explicitly:

```go
dcron.New(sqlDB,
	dcron.WithDedicatedLockDriver("pgx", // stdlib registers this name
		"postgres://app:pw@db-host:5432/app?sslmode=disable"),
)
```

`WithDedicatedLockDSN(dsn)` remains the lib/pq shorthand (it opens driver
name `"postgres"`).

## Parity checklist (lib/pq vs pgx/stdlib)

| Concern | lib/pq | pgx/stdlib | Notes |
| :-- | :-- | :-- | :-- |
| Driver name | `"postgres"` | `"pgx"` | Use `WithDedicatedLockDriver(name, dsn)` to pick; `WithDedicatedLockDSN` implies `"postgres"` |
| Advisory locks (`pg_try_advisory_lock`, etc.) | ✅ | ✅ | Session-scoped on both; identical semantics |
| `pg_backend_pid()` probe | ✅ | ✅ | Only used in tests |
| Parameter binding in history SQL | `$1..$n` | `$1..$n` | store package uses positional params exclusively |
| Binary protocol perf benefits | ❌ | ✅ | Lost via database/sql; acceptable for 1 row/job/min workloads |

## Why not `NewWithPool(*pgxpool.Pool)` yet (#24)

1. **NFR-403 isolation**: an exported signature taking `*pgxpool.Pool` makes
   every library consumer link pgx even when they use lib/pq — exactly what
   NFR-403 forbids. The AC's own wording ("dependency isolated so
   database/sql users don't link it") rules out the naive constructor.
2. **Correct shape exists already**: `dcron.NewWithBackend(dcron.LockBackend,
   opts...)` accepts any implementation of four methods. A future
   `contrib/pgxdcron` module can implement `LockBackend` over `pgxpool`
   directly (native binary protocol, no database/sql hop) while keeping the
   dependency in the contributor module.
3. **Both drivers exercised in CI** lands with the integration suite
   (`test/integration-testcontainers`, issue #28): the same scenario matrix
   runs once per driver.

Until (2) ships, pgx users get full functionality through stdlib registration;
what they give up is pgx's batch/binary-protocol performance inside the
scheduler itself — negligible at cron cadences.

## Single-replica parity note (#27)

One replica must behave like an in-process cron: first fire suffers no extra
latency beyond the first poll tick (`WithPollInterval`; default 5s, safe to set
to `time.Second`). Fire-time comparison against robfig/cron for identical
specs is tracked as AC-08 in the parity test of issue #27 and lives with the
integration suite.
