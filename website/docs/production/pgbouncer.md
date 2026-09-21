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

### Solution 1: Dedicated Connection for `d-cron` (Recommended)

The cleanest production pattern is to bypass transaction-mode PgBouncer for `d-cron`'s leader lock connection while using PgBouncer for your normal application queries:

```go
// 1. App queries use PgBouncer (Transaction Pool)
appDB, _ := sql.Open("pgx", "postgres://user:pass@pgbouncer:6432/myapp_db?sslmode=disable")

// 2. d-cron leader lock uses Direct PostgreSQL Connection (Direct Port 5432)
cronLockDB, _ := sql.Open("pgx", "postgres://user:pass@postgres-direct:5432/myapp_db?sslmode=disable")

// Initialize d-cron with dedicated direct connection
scheduler, _ := dcron.New(
    cronLockDB,
    dcron.WithNamespace("orders-service"),
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
cronDB, _ := sql.Open("pgx", "postgres://user:pass@pgbouncer:6432/myapp_cron_db?sslmode=disable")
scheduler, _ := dcron.New(cronDB)
```

---

## 📊 Summary Comparison

| Connection Approach | PgBouncer Mode | Advisory Lock Compatible? | Overhead |
| :--- | :--- | :--- | :--- |
| **Direct Postgres (5432)** | N/A | **100% Fully Compatible** | 1 DB connection per pod |
| **PgBouncer Session Alias** | `session` | **100% Fully Compatible** | 1 session connection per pod |
| **PgBouncer Transaction** | `transaction` | ❌ **NOT Compatible** | Advisory locks drop between queries |
