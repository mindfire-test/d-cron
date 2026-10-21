---
id: advisory-locks
title: PostgreSQL Advisory Locks
sidebar_label: Advisory Lock Backend
---

# 🔒 PostgreSQL Advisory Locks

`d-cron` leverages **PostgreSQL Session-Bound Advisory Locks** (`pg_try_advisory_lock`) to achieve ultra-lightweight, zero-migration distributed coordination.

Unlike traditional table locks or row locks (`SELECT FOR UPDATE`), advisory locks exist purely in PostgreSQL memory engine structures and do not create tuple bloat or write lock rows to disk.

---

## 🔬 How Advisory Locks Work

PostgreSQL provides two advisory lock functions used by `d-cron`:

| SQL Function | Scope | Behavior |
| :--- | :--- | :--- |
| `pg_try_advisory_lock(key BIGINT)` | Session-Bound | Attempts to acquire a 64-bit lock instantly. Returns `true` on success, `false` immediately without blocking if acquired elsewhere. |
| `pg_advisory_unlock(key BIGINT)` | Session-Bound | Releases the advisory lock bound to the active database connection session. |

### Why Session-Bound?

Session-bound advisory locks are attached to the **underlying TCP connection session** between your Go application node and PostgreSQL:

```
┌─────────────────────────────────┐                 ┌─────────────────────────────────┐
│       App Node (Replica A)      │                 │       App Node (Replica B)      │
│  Dedicated DB Connection (Conn1) │                 │  Dedicated DB Connection (Conn2) │
└────────────────┬────────────────┘                 └────────────────┬────────────────┘
                 │                                                   │
                 │ pg_try_advisory_lock(84920491)                    │ pg_try_advisory_lock(84920491)
                 │  ==> TRUE (Acquired)                              │  ==> FALSE (Leader Active)
                 ▼                                                   ▼
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                               PostgreSQL Memory Engine                              │
│  Advisory Lock Table: [Key: 84920491, SessionID: 10492 (Conn1)]                    │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

1. **Automatic Cleanup on Crash**: If Replica A crashes, power-cycles, or experiences network partition, PostgreSQL automatically drops `Conn1` and **releases the advisory lock within seconds** (or TCP keepalive timeout).
2. **Zero Disk I/O**: Acquiring or checking an advisory lock touches zero table buffers and generates zero WAL (Write-Ahead Logging) overhead.

---

## 🔑 Key Resolution & Hashing

`d-cron` derives a deterministic 64-bit advisory lock key from your configured namespace string (e.g. `myapp-scheduler`):

```go
// LockKey derives the deterministic 64-bit advisory-lock key from namespace:
// key = int64 from the first 8 bytes of sha256("d-cron:v1:" + namespace)
func LockKey(namespace string) int64 {
    h := sha256.Sum256([]byte("d-cron:v1:" + namespace))
    k := int64(binary.BigEndian.Uint64(h[:8]))
    if k == 0 {
        return 1
    }
    return k
}
```

This guarantees that:
- Independent microservices running on the same Postgres database use different namespace strings and never collide.
- All replicas of the *same* microservice generate the exact same 64-bit lock key to contend for single-leader status.

---

## ⚡ Advisory Locks vs Row-Level Locks

| Metric / Feature | Row-Level Locks (`SELECT FOR UPDATE`) | Session Advisory Locks (`d-cron`) |
| :--- | :--- | :--- |
| **Disk Bloat / Dead Tuples** | High (Writes tuple xmin/xmax) | **Zero** |
| **WAL Generation** | Yes | **No** |
| **Automatic Cleanup on Crash** | Relies on transaction abort | **Automatic (Session Drop)** |
| **Connection Overhead** | Requires active open transaction | **Uses 1 Dedicated DB Connection** |
| **Lock Latency** | ~2–5 ms | **< 0.5 ms** |

---

## ⚙️ Elector Backend Interface

`d-cron` defines a clean interface for advisory lock operations behind the elector (`internal/elector.Backend`), allowing the leadership state machine to be exercised in tests with deterministic fakes without requiring live PostgreSQL:

```go
type Backend interface {
    TryLock(ctx context.Context, key int64) (acquired bool, pid int, err error)
    HoldsLock(ctx context.Context, key int64) (bool, error)
    Release(ctx context.Context, key int64) (bool, error)
    Close() error
}
```

The production implementation (`NewStdBackend`) wraps a dedicated session-stable `*sql.Conn` that holds the 64-bit advisory lock for the entire leadership term.
