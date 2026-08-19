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

| Factor | `robfig/cron` | `gocron` | `asynq` | `river` | **`d-cron`** |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Architecture** | In-Process | In-Process | Task Queue | Task Queue | **In-Process** |
| **Infra Required** | None | Redis | Redis | Postgres + Tables | **Postgres (No extra tables)** |
| **Coordination** | None | Per-Job Lock Race | Queue Enqueue | Leader Election | **Leader Election** |
| **Leader Election** | ✗ No | ✗ No | ✗ No | ✓ Yes | **✓ Yes** |
| **Split-Brain Fencing**| ✗ No | ✗ No | ✗ No | ✗ No | **✓ Yes** |
| **License** | Open Source | Open Source | Open Source | Paid | **Open Source** |

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

	// Initialize d-cron. WithNamespace scopes the lock; session-stability
	// (SDS §3.4) is a Phase-1 requirement (not yet on the v0.x scaffolding).
	scheduler, err := dcron.New(db,
		dcron.WithNamespace("billing"),
		dcron.WithPollInterval(3*time.Second),
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

	// Start leader election & the scheduler loop. Stop is bounded by
	// WithDrainTimeout (default 30s).
	ctx := context.Background()
	if err := scheduler.Start(ctx); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}
	defer scheduler.Stop(ctx)
}
```

---

## 4. Correctness Model & Guarantees

### What `d-cron` Guarantees
- **Under Normal Operation**: At-most-once execution per scheduled fire time across all replicas.
- **Under Leader Failover**: Automatic standby promotion within 1 poll interval on process exit.
- **Split-Brain Protection**: Monotonic leader epoch tokens (`LeaderEpoch`) injected into job `context.Context` to fence stale database writes.
- **Single Replica Parity**: If deployed with `N=1`, degrades gracefully to behave as an ordinary in-process cron.

### Failure Mode Behaviour
| Failure Scenario | System Behaviour |
| :--- | :--- |
| **Graceful Stop** | Advisory lock explicitly released via `pg_advisory_unlock`; standby promoted within 1 poll interval. |
| **Process SIGKILL** | Kernel closes TCP socket; PostgreSQL reaps backend session and frees lock; standby promoted within 1 poll interval. |
| **Network Cut** | PostgreSQL backend blocks in `recv()`. **Lock is NOT released until TCP Keepalives expire** (requires operator configuration below). |
| **Transaction Pooler** | Transaction-mode poolers hand 1 server session to multiple clients, breaking lock semantics. Requires operator assertion (`WithSessionStableConnection`) or dedicated DSN. |

---

## 5. Mandatory Operator Configuration

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

## 6. Development & Workflow

### Required Tooling
| Tool | Version | Verification |
| :--- | :--- | :--- |
| Go | `go 1.23` (go.mod) | `go version` |
| gofumpt | `v0.10.0` | `gofumpt -version` |
| golangci-lint | `v1.64.8` | `golangci-lint version` |

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
*Example*: `feat(elector): add postgres advisory lock acquirer`

---
