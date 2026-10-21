---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
---

# Troubleshooting

This page provides solutions for common issues encountered when integrating or deploying `d-cron`.

---

## Leadership and Locking Issues

### Problem: Leadership repeatedly cycling or dropping unexpectedly

**Solutions:**

- **Verify connection pooler mode**: If you are using PgBouncer, ensure it is not operating in transaction mode for `d-cron`. Use a direct PostgreSQL connection for the lock via `dcron.WithDedicatedLockDSN("postgres://...")` or configure a dedicated PgBouncer database alias in session mode. See the [Production Guide](./production/kubernetes.md#3-pgbouncer-and-connection-pooling).
- **Prevent connection recycling**: If passing a shared `*sql.DB` connection pool, ensure `db.SetConnMaxLifetime(0)` is set on the lock connection handle so Go's connection pool does not close the session periodically.
- **Check TCP keepalive settings**: Ensure PostgreSQL connection keepalives (`keepalives=1&keepalives_idle=10&keepalives_interval=3&keepalives_count=3`) are configured in the DSN to detect dropped connections quickly.

### Problem: Jobs executing simultaneously on multiple pods (Duplicate runs)

**Solutions:**

- **Check namespace configuration**: All replicas of the same service must share the exact same namespace string. `d-cron` derives the 64-bit advisory lock key from `dcron.WithNamespace("service-name")`. If pod A uses `"service-a"` and pod B uses `"service-b"`, both will elect themselves leader.
- **Verify single database instance**: Ensure all replicas connect to the same PostgreSQL primary database. If replicas connect to different read replicas or partitioned databases, each will acquire the advisory lock independently.

---

## Fencing and Concurrency Issues

### Problem: Fencing rejection error ("fencing error: epoch X is stale")

**Solutions:**

- **Recognize intended safety behavior**: This error indicates that a long GC pause, process freeze, or network partition occurred. PostgreSQL dropped the session lock, promoted another standby replica to a higher epoch, and the demoted replica attempted to execute a database write. The fencing mechanism successfully prevented data corruption.
- **Abort stale handlers**: Handle `dcron.Fence(ctx, tx)` errors by rolling back the transaction and aborting the handler cleanly.
- **Tune drain timeouts**: Ensure `dcron.WithDrainTimeout(d)` allows sufficient grace period for in-flight jobs to complete during graceful shutdown.

### Problem: High database CPU or lock polling spikes

**Solutions:**

- **Adjust poll interval**: Standby replicas poll `TryLock` periodically. Ensure `dcron.WithPollInterval` is not set to an aggressive sub-second interval. The recommended default is `3s` to `5s`.
- **Verify jitter**: `d-cron` adds randomized jitter (±20%) to polling intervals automatically to prevent synchronized polling storms. Avoid overriding this with custom fixed timers.

---

## Execution and Job Issues

### Problem: Missed jobs after restart or deployment

**Solutions:**

- **Configure missed run policy**: By default, `d-cron` uses `MissedSkip`. If jobs must catch up after an application restart, configure `dcron.WithMissedRunPolicy(dcron.MissedCatchUp)` and `dcron.WithHistory(...)`.
- **Set lookback limit**: Use `dcron.WithMaxLookback(24 * time.Hour)` to prevent executing runs that are too old.

### Problem: Long-running jobs blocking subsequent schedules

**Solutions:**

- **Configure overlap policy**: By default, overlapping runs of the same job are skipped (`dcron.OverlapSkip`). If you need jobs to queue or run concurrently, pass `dcron.WithOverlapPolicy(dcron.OverlapQueue)` or `dcron.WithOverlapPolicy(dcron.OverlapAllow)`.
- **Set timeouts**: Enforce maximum execution duration per job using `dcron.WithTimeout(10 * time.Minute)`.

---

## Getting Help

If you are still experiencing issues:

1. Check the [Introduction](./intro.md) for basic integration examples
2. Enable structured debug logging: pass `dcron.WithLogger(slog.Default())` to `dcron.New`
3. Inspect Prometheus metrics: observe `dcron_leadership_status`, `dcron_leader_transitions_total`, and `dcron_job_executions_total`
4. Check the [GitHub Issues](https://github.com/mindfire-test/d-cron/issues) for similar problems
