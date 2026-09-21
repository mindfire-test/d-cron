---
id: history-store
title: Audit & History Store
sidebar_label: History Store
---

# 📜 Audit & History Store

`d-cron` supports pluggable audit logging stores to record job execution logs, execution status, latencies, and error messages to PostgreSQL or ORMs like GORM and Sqlx.

---

## 💾 Enabling History Logging

To persist job execution logs, pass a `HistoryStore` implementation to `dcron.WithHistoryStore`:

```go
import (
	"github.com/mindfiredigital/d-cron/dcron"
	"github.com/mindfiredigital/d-cron/dcron/history"
)

func main() {
	// Create PostgreSQL History Store using database/sql
	store := history.NewPostgresStore(db)

	scheduler, err := dcron.New(
		db,
		dcron.WithHistoryStore(store),
	)
    // ...
}
```

---

## 🗄️ Database Table Schema

Ensure your database contains the `dcron_job_history` table (see [Installation SQL Setup](../installation.md#optional-history--audit-store-setup)):

```sql
CREATE TABLE IF NOT EXISTS dcron_job_history (
    id BIGSERIAL PRIMARY KEY,
    job_name VARCHAR(255) NOT NULL,
    schedule VARCHAR(100) NOT NULL,
    instance_id VARCHAR(255) NOT NULL,
    leader_epoch BIGINT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    duration_ms BIGINT NOT NULL,
    status VARCHAR(50) NOT NULL,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 🔌 GORM & Sqlx Adapters

### GORM Adapter

If your application uses [GORM](https://gorm.io):

```go
import "github.com/mindfiredigital/d-cron/dcron/history/gormstore"

gormStore := gormstore.New(gormDB)

// GORM can automatically migrate the history model if auto-migration is enabled:
gormStore.AutoMigrate()

scheduler, _ := dcron.New(db, dcron.WithHistoryStore(gormStore))
```

### In-Memory Testing Store

For unit testing without a live database:

```go
memStore := history.NewInMemoryStore()
scheduler, _ := dcron.New(db, dcron.WithHistoryStore(memStore))

// Inspect records after test run
records := memStore.GetHistory("my-test-job")
fmt.Printf("Total runs recorded: %d\n", len(records))
```
