---
id: troubleshooting
title: Troubleshooting & FAQ
sidebar_label: Troubleshooting
---

# 🔍 Troubleshooting Guide

This guide helps diagnose and resolve common issues encountered when integrating or deploying `d-cron`.

---

## 🚨 Common Issues & Resolutions

### 1. Lock Contention / Advisory Lock Dropping in PgBouncer

**Symptom:**
Nodes repeatedly cycle leadership status, emitting log messages like:
`[WARN] Lost session advisory lock connection. Demoting to standby...`

**Cause:**
PgBouncer is operating in **Transaction Pooling Mode** (`pool_mode = transaction`). In transaction pooling mode, PostgreSQL connections are recycled after every transaction, causing session-bound advisory locks (`pg_try_advisory_lock`) to drop.

**Resolution:**
- Use a direct PostgreSQL connection (port `5432`) for `d-cron`'s leader lock handle.
- Or configure a dedicated PgBouncer database alias with `pool_mode = session`.
- See the [PgBouncer Integration Guide](./production/pgbouncer.md) for step-by-step instructions.

---

### 2. Jobs Running Concurrently on Multiple Pods (Duplicate Executions)

**Symptom:**
The same scheduled job executes simultaneously on two different pod replicas.

**Cause:**
Different application pod replicas are configured with different namespace strings (e.g., Replica 1 uses `"service-a"` while Replica 2 uses `"service-b"`). Because namespace strings determine the 64-bit advisory lock key, different strings result in separate lock keys and multiple active leaders.

**Resolution:**
Ensure all replicas of the same microservice share the exact same namespace string:
```go
// Both pods must use the exact same namespace string!
scheduler, err := dcron.New(db, dcron.WithNamespace("orders-service"))
```

---

### 3. Fencing Token Errors (`stale leader epoch`)

**Symptom:**
Database write fails with error: `fencing error: epoch X is stale`.

**Cause:**
A node experienced a long GC pause or network partition. During the delay, PostgreSQL dropped its lock session, a new leader promoted with a higher epoch (e.g., Epoch 5), and the old node resumed processing with an older token (Epoch 4).

**Resolution:**
This is **intended safety behavior!** The fencing mechanism successfully prevented a duplicate database write from a stale leader. The job handler should abort cleanly when encountering fencing errors.

---

### 4. High Lock Polling Spikes

**Symptom:**
PostgreSQL CPU utilization shows spikes during standby polling checks.

**Cause:**
`WithPollInterval` is set to an excessively aggressive value (e.g. `10ms`).

**Resolution:**
Set `WithPollInterval` to a production-appropriate interval such as `3s` or `5s`:
```go
scheduler, err := dcron.New(db, dcron.WithPollInterval(3 * time.Second))
```

---

## 📝 Diagnostic Logging Checklist

When filing an issue or debugging:
- [x] Enable structured debug logging via `log/slog`.
- [x] Verify current leader ID using `scheduler.IsLeader()`.
- [x] Inspect `/metrics` or Prometheus `dcron_is_leader` and `dcron_leader_epoch` metrics.
- [x] Verify PostgreSQL connection parameters (`SetConnMaxLifetime(0)` on lock handle).
