---
id: fencing-tokens
title: Split-Brain & Fencing Tokens
sidebar_label: Split-Brain & Fencing
---

# Split-Brain Prevention & Fencing Tokens

In distributed systems, GC pauses, heavy OS swapping, or temporary network partitions can cause a node to experience a **split-brain** state: a node *thinks* it is still the leader while a new leader has already been elected.

`d-cron` addresses this fundamental distributed systems challenge using **Monotonic Fencing Tokens**.

---

## The Split-Brain Threat

Without fencing, a delayed node (Replica A) might resume executing a long-running scheduled job (such as generating daily invoices) while Replica B has already assumed leadership and triggered the same job:

```
[Replica A (Old Leader)] ────(GC Pause 10s)────► Executes Job (Epoch 4) ───► Database Write!  (Duplicate)
                                                      ▲
[Replica B (New Leader)] ───(Elected Leader)────► Executes Job (Epoch 5) ───► Database Write!
```

---

## How Fencing Tokens Work

`d-cron` attaches a strictly increasing **Monotonic Leader Epoch** and an execution **Idempotency Key** to the `context.Context` of every job execution.

Whenever a node acquires leadership, it advances the cluster epoch (stored in `{schema}.leader_epoch`).

### Reading Fencing Tokens in Go Handlers

Inside your Go job handlers, extract fencing metadata directly from `ctx`:

```go
scheduler.Add("generate-invoices", "0 1 * * *", func(ctx context.Context) error {
    epoch := dcron.Epoch(ctx)
    idempotencyKey := dcron.IdempotencyKey(ctx)

    slog.Info("Job running",
        "leader_epoch", epoch,
        "idempotency_key", idempotencyKey,
    )

    return executeInvoiceBatch(ctx, epoch)
})
```

---

## PostgreSQL Transaction Fencing (`dcron.Fence`)

When `WithHistory` is enabled, `d-cron` automatically records the latest active leader epoch in `{schema}.leader_epoch`.

Inside any scheduled job handler, call `dcron.Fence(ctx, tx)` inside your database transaction. It verifies under a `FOR SHARE` row-level lock that the context's epoch still matches the active database epoch:

```go
scheduler.Add("process-billing-ledger", "0 0 * * *", func(ctx context.Context) error {
    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // 1. Validate epoch against PostgreSQL leader_epoch table
    if err := dcron.Fence(ctx, tx); err != nil {
        // Demoted leader! Roll back and safely abort without side effects.
        return fmt.Errorf("fencing rejection: %w", err)
    }

    // 2. Perform business logic safely
    _, err = tx.ExecContext(ctx, "UPDATE invoices SET status = 'processed' WHERE due_date <= now()")
    if err != nil {
        return err
    }

    return tx.Commit()
})
```

If another pod was promoted to leader while this handler was paused, `dcron.Fence` rejects the transaction with:
```
dcron: fenced write rejected: ctx epoch 4 != current db epoch 5
```

---

## Database Optimistic Fencing Pattern

For systems without `dcron.Fence` or custom databases, store the highest processed epoch in your own application schema:

```sql
CREATE TABLE job_execution_fence (
    job_name VARCHAR(100) PRIMARY KEY,
    last_processed_epoch BIGINT NOT NULL
);
```

Before committing state changes, execute a conditional update:

```go
func executeInvoiceBatch(ctx context.Context, currentEpoch int64) error {
    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // Conditional UPDATE: fails if a higher epoch has already committed
    res, err := tx.ExecContext(ctx, `
        UPDATE job_execution_fence 
        SET last_processed_epoch = $1 
        WHERE job_name = 'generate-invoices' AND last_processed_epoch < $1`,
        currentEpoch,
    )
    if err != nil {
        return err
    }

    rowsAffected, _ := res.RowsAffected()
    if rowsAffected == 0 {
        // Stale leader execution! Safely abort without duplicate side-effects.
        return fmt.Errorf("fencing error: epoch %d is stale or already executed", currentEpoch)
    }

    return tx.Commit()
}
```

---

## External & Redis Distributed Fencing

When writing to Redis or external key-value stores from a scheduled job, use `dcron.Epoch(ctx)` or `dcron.IdempotencyKey(ctx)` as an atomic conditional guard or lock value:

```go
func RedisFencedJob(ctx context.Context, rdb *redis.Client) error {
    epoch := dcron.Epoch(ctx)
    if epoch == 0 {
        return errors.New("missing epoch in context")
    }

    key := "fencing:job:daily-cache-sync"
    // Atomic SET if epoch is strictly greater using Lua script or Redlock
    script := `
        local cur = redis.call('get', KEYS[1])
        if not cur or tonumber(ARGV[1]) > tonumber(cur) then
            redis.call('set', KEYS[1], ARGV[1])
            return 1
        else
            return 0
        end
    `
    res, err := rdb.Eval(ctx, script, []string{key}, epoch).Int()
    if err != nil {
        return err
    }
    if res == 0 {
        return fmt.Errorf("redis fencing abort: epoch %d is stale", epoch)
    }

    return nil
}
```

---

## Outgoing HTTP API Idempotency Fencing

For jobs calling third-party payment gateways (Stripe, Adyen, Twilio), send `dcron.IdempotencyKey(ctx)` in standard idempotency headers:

```go
req, err := http.NewRequestWithContext(ctx, "POST", "https://api.stripe.com/v1/charges", body)
if err != nil {
    return err
}
req.Header.Set("Idempotency-Key", dcron.IdempotencyKey(ctx))
```

This guarantees that even in split-brain edge cases, third-party payment gateways reject duplicate charges automatically.

---

## Summary

1. **Context Carrier**: Every `d-cron` job receives a `ctx` populated with `dcron.Epoch(ctx)` and `dcron.IdempotencyKey(ctx)`.
2. **Monotonic Guarantee**: Epochs strictly increment on every failover, ensuring older leaders can never produce higher tokens than newer leaders.
3. **Storage Fencing**: Combining Go context tokens with database conditional updates (or `dcron.Fence(ctx, tx)`) guarantees 100% safety even under catastrophic network splits.
