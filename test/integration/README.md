# d-cron integration suite (issue #28)

Black-box tests against **real PostgreSQL** via testcontainers. Lives in its
own Go module so `testcontainers-go` never appears in the root go.mod that
library consumers download. All files carry the `integration` build tag.

## Run

```sh
# one-time: generate go.sum (network-bound; testcontainers pulls docker libs)
go mod tidy

# with Docker running:
go test -tags integration -v ./...

# or against your own PostgreSQL:
DCRON_TEST_DSN=postgres://user:pw@host:5432/db?sslmode=disable \
  go test -tags integration ./...
```

No Docker and no DSN ⇒ every test skips; CI without a daemon stays green.

## Coverage vs issue #28 acceptance criteria

| AC | Test | Status |
| :-- | :-- | :-- |
| Unsafe-start gates (no assertion / MaxOpenConns=1) | `TestGates_RefusesUnsafeStart` | ✅ |
| N schedulers → exactly-one promotion after leader death | `TestLeaderFailover_PromotesExactlyOneStandby` | ✅ |
| C-07 re-entrant advisory lock regression | `TestC07_ReentrantAdvisoryLock` | ✅ |
| 10 replicas race Migrate from empty (#34) | `TestMigrate_TenReplicasConcurrent` | ✅ |
| AC-09 zero tables after lifecycle | `TestAC09_ZeroTablesAfterLifecycle` | ✅ |
| PgBouncer transaction-mode corruption | `TestPgBouncerTransactionPooling` | ⏳ env-gated stub (`DCRON_TEST_PGBOUNCER_DSN`) |
| AC-02b partitioned leader holds lock | `TestAC02b_PartitionedLeaderHoldsLock` | ⏳ env-gated stub (needs toxiproxy) |

Driver matrix (#24): run the suite once with the pgx stdlib driver registered —
the DSN-driven design means the same scenarios cover both drivers.
