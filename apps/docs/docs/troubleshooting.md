---
id: troubleshooting
title: Troubleshooting & FAQ
sidebar_label: Troubleshooting & FAQ
---

# 🔍 Troubleshooting Guide & FAQ

This guide helps diagnose and resolve common issues encountered when integrating or deploying `d-cron`, and answers frequently asked questions.

---

## 🚨 Common Issues & Resolutions

### 1. Lock Contention / Advisory Lock Dropping in PgBouncer

**Symptom:**
Nodes repeatedly cycle leadership status, emitting log messages like:
`[WARN] Lost session advisory lock connection. Demoting to standby...`

**Cause:**
PgBouncer is operating in **Transaction Pooling Mode** (`pool_mode = transaction`). In transaction pooling mode, PostgreSQL connections are recycled after every transaction, causing session-bound advisory locks (`pg_try_advisory_lock`) to drop.

**Resolution:**
- Use a direct PostgreSQL connection (`dcron.WithDedicatedLockDSN`) for `d-cron`'s leader lock handle.
- Or configure a dedicated PgBouncer database alias with `pool_mode = session`.
- See the [Production & Kubernetes Guide](./production/kubernetes.md#3-pgbouncer-and-connection-pooling) for step-by-step instructions.

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
- [x] Verify leadership status using `scheduler.Leadership()` and `scheduler.InstanceID()`.
- [x] Inspect `/metrics` or Prometheus `dcron_is_leader` and `dcron_leader_transitions_total` metrics.
- [x] Verify PostgreSQL connection parameters (`SetConnMaxLifetime(0)` on lock handle).

---

## ❓ Frequently Asked Questions (FAQ)

### Q: Why use `d-cron` instead of `gocron`, `asynq`, or `robfig/cron`?
**A:** In-process cron libraries like `robfig/cron` or `gocron` do not natively coordinate single-leader election across multiple scaled pod replicas, resulting in duplicate job runs or database connection lock spikes at trigger boundaries. Task queues like `asynq` or `river` solve coordination but require introducing extra infrastructure (Redis or custom PostgreSQL job payload queue tables with schema migrations). `d-cron` provides safe distributed single-leader coordination **in-process using PostgreSQL advisory locks with zero new database tables.**

---

### Q: Does `d-cron` create any database tables by default?
**A:** **No.** By default, `d-cron` operates purely using PostgreSQL Session Advisory Locks in Postgres shared memory structures (`pg_try_advisory_lock`). It requires zero DDL schema migrations. Tables are only created if you explicitly enable optional execution history (`dcron.WithHistory(retention)`).

---

### Q: What happens if the active Leader node crashes or loses network connectivity?
**A:** The TCP session between the Leader node and PostgreSQL terminates. PostgreSQL automatically drops the session-bound advisory lock. Within the configured polling interval (default: 5 seconds with jitter), one of the remaining Standby nodes acquires the advisory lock, increments the monotonic leader epoch, and takes over scheduling seamlessly.

---

### Q: Is `d-cron` compatible with PgBouncer?
**A:** Yes! Because advisory locks are session-bound, you can either configure `dcron.WithDedicatedLockDSN` to point directly to PostgreSQL (bypassing PgBouncer for the lock connection), or configure a dedicated PgBouncer database alias in `session` pool mode and pass `dcron.WithSessionStableConnection()`. See the [Production Guide](./production/kubernetes.md#3-pgbouncer-and-connection-pooling) for details.

---

### Q: Can I run high-frequency jobs (e.g. every 1 second)?
**A:** Yes. Pass `dcron.WithSecondsField()` to enable 6-field cron expressions with second-level precision. Sub-millisecond internal scheduling overhead makes high-frequency cron schedules fast and efficient.

---

### Q: How do I contribute or report bugs?
**A:** `d-cron` is 100% open source under the MIT license! Contributions, issues, and feature requests are welcome on [GitHub](https://github.com/mindfire-test/d-cron).
