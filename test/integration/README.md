# d-cron integration suite

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

## Test Coverage

| Scenario | Test | Status |
| :-- | :-- | :-- |
| Unsafe-start gates (no assertion / MaxOpenConns=1) | `TestGates_RefusesUnsafeStart` | ✅ |
| N schedulers → exactly-one promotion after leader death | `TestLeaderFailover_PromotesExactlyOneStandby` | ✅ |
| Re-entrant advisory lock regression | `TestC07_ReentrantAdvisoryLock` | ✅ |
| 10 replicas race Migrate from empty | `TestMigrate_TenReplicasConcurrent` | ✅ |
| Zero tables after lifecycle | `TestAC09_ZeroTablesAfterLifecycle` | ✅ |
| PgBouncer transaction-mode corruption | `TestPgBouncerTransactionPooling` | ⏳ env-gated stub (`DCRON_TEST_PGBOUNCER_DSN`) |
| Partitioned leader holds lock | `TestAC02b_PartitionedLeaderHoldsLock` | ⏳ env-gated stub (needs toxiproxy) |

Driver matrix: run the suite once with the pgx stdlib driver registered —
the DSN-driven design means the same scenarios cover both drivers.
