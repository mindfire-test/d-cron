---
id: pgx-driver
title: pgx/v5 & database/sql Setup
sidebar_label: pgx/v5 Setup
---

# 🐘 `pgx/v5` & `database/sql` Configuration

`d-cron` supports both standard `database/sql` (`lib/pq`, `pgx/v5/stdlib`) and native `pgx/v5` pool connections (`*pgxpool.Pool`).

---

## 🔌 Setup with `pgx/v5` (stdlib mode)

`pgx` is the standard PostgreSQL driver for modern Go applications. When using `pgx/v5/stdlib`:

```go
package main

import (
	"database/sql"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/mindfiredigital/d-cron/dcron"
)

func main() {
	// Configure DSN connection string
	dsn := "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable"
	
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("failed to open postgres: %v", err)
	}

	// Recommended connection pool settings for d-cron DB handle
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0) // Do not forcibly close session connection

	scheduler, err := dcron.New(
		db,
		dcron.WithNamespace("my-service"),
	)
	if err != nil {
		log.Fatalf("failed to init scheduler: %v", err)
	}

	scheduler.Start(context.Background())
}
```

---

## ⚡ Setup with `lib/pq`

If your legacy codebase uses `github.com/lib/pq`:

```go
import (
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/mindfiredigital/d-cron/dcron"
)

func main() {
	db, err := sql.Open("postgres", "user=postgres dbname=myapp_db sslmode=disable")
	scheduler, err := dcron.New(db)
}
```

---

## 🛠️ Connection Lifespan Best Practices

Because advisory locks are attached to PostgreSQL TCP sessions:

1. **`SetConnMaxLifetime(0)`**: Do not configure max lifetime connection recycling on the dedicated `*sql.DB` connection handle used by `d-cron`. If the database driver forcibly closes the connection to recycle it, the advisory lock will drop and trigger unnecessary failover.
2. **Dedicated Single Connection**: `d-cron` internally acquires and pinpoints 1 dedicated connection for leadership lock management while leaving remaining pool connections available for standard application queries.
