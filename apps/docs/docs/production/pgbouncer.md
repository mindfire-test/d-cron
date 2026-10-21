---
id: pgbouncer
title: PgBouncer & Connection Pooling
sidebar_label: PgBouncer Setup
---

# 🐘 PgBouncer & Connection Pooling Integration

PostgreSQL advisory locks used by `d-cron` are **Session-Bound**. Standard session advisory locks rely on a stable, persistent TCP connection between the application and PostgreSQL.

When using database connection poolers like **PgBouncer**, **Supavisor**, or AWS RDS Proxy, special configuration is required to prevent advisory locks from being improperly pooled across client sessions.

---

## ⚠️ The PgBouncer Transaction Mode Trap

PgBouncer is commonly deployed in **Transaction Pooling Mode** (`pool_mode = transaction`). In this mode:
- A server connection is assigned to a client *only for the duration of a single transaction*.
- Subsequent queries outside transactions may be assigned to a different backend connection.
- **Session-bound advisory locks will fail or be lost when the connection is recycled!**

---

## 🛠️ Recommended Solutions

### Solution 1: Dedicated Direct Lock DSN (Recommended)

The cleanest production pattern is to let normal application queries flow through PgBouncer while configuring `d-cron` to open an independent, dedicated connection directly to PostgreSQL for the advisory lock:

```go
// 1. Normal application queries use PgBouncer in transaction mode
appDB, _ := sql.Open("postgres", "postgres://user:pass@pgbouncer:6432/myapp_db?sslmode=disable")

// 2. d-cron leader lock connects directly to PostgreSQL (port 5432)
scheduler, _ := dcron.New(
    appDB,
    dcron.WithNamespace("orders-service"),
    dcron.WithDedicatedLockDSN("postgres://user:pass@postgres-direct:5432/myapp_db?sslmode=disable"),
)
```

---

### Solution 2: PgBouncer Session Pool Mode

If all connections must flow through PgBouncer, create a dedicated PgBouncer database alias in **Session Pooling Mode**:

**`pgbouncer.ini` Configuration:**

```ini
[databases]
; Normal app connection (transaction mode)
myapp_db = host=postgres-db port=5432 dbname=myapp_db pool_mode=transaction

; Dedicated d-cron alias (session mode)
myapp_cron_db = host=postgres-db port=5432 dbname=myapp_db pool_mode=session
```

**Go Application Setup:**

```go
cronDB, _ := sql.Open("postgres", "postgres://user:pass@pgbouncer:6432/myapp_cron_db?sslmode=disable")
scheduler, _ := dcron.New(cronDB,
    dcron.WithNamespace("orders-service"),
    dcron.WithSessionStableConnection(),
)
```

---

## 📊 Summary Comparison

| Connection Approach | PgBouncer Mode | Advisory Lock Compatible? | Overhead |
| :--- | :--- | :--- | :--- |
| **Direct Postgres (5432)** | N/A | **100% Fully Compatible** | 1 DB connection per pod |
| **PgBouncer Session Alias** | `session` | **100% Fully Compatible** | 1 session connection per pod |
| **PgBouncer Transaction** | `transaction` | ❌ **NOT Compatible** | Advisory locks drop between queries |
