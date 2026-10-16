---
id: fencing-tokens
title: Split-Brain & Fencing Tokens
sidebar_label: Fencing Tokens
---

# 🛡️ Split-Brain Prevention & Fencing Tokens

In distributed systems, GC pauses, heavy OS swapping, or temporary network partitions can cause a node to experience a **split-brain** state: a node *thinks* it is still the leader while a new leader has already been elected.

`d-cron` addresses this fundamental problem using **Monotonic Fencing Tokens**.

---

## ⚡ The Split-Brain Threat

Without fencing, a delayed node (Replica A) might resume executing a long-running scheduled job (such as generating daily invoices) while Replica B has already assumed leadership and triggered the same job:

```
[Replica A (Old Leader)] ────(GC Pause 10s)────► Executes Job (Epoch 4) ───► Database Write! ❌ (Duplicate)
                                                      ▲
[Replica B (New Leader)] ───(Elected Leader)────► Executes Job (Epoch 5) ───► Database Write!
```

---

## 🔑 How Fencing Tokens Work

`d-cron` attaches a strictly increasing **Monotonic Leader Epoch** to the `context.Context` of every job execution:

```go
type FencingToken struct {
    Epoch      int64  // Monotonically increasing number (1, 2, 3...)
    LeaderID   string // Node instance ID (e.g. "pod-replica-02")
}
```

Whenever a node acquires leadership, it increments the global epoch stored in its advisory lock session context.

### Reading Fencing Tokens in Job Handlers

Inside your Go job handlers, extract the fencing token using `dcron.GetFencingToken(ctx)`:

```go
scheduler.Add("generate-invoices", "0 1 * * *", func(ctx context.Context) error {
    // Extract fencing metadata
    token, ok := dcron.GetFencingToken(ctx)
    if !ok {
        return errors.New("missing fencing token in context")
    }

    fmt.Printf("Job running under Leader Epoch: %d by Node: %s\n", token.Epoch, token.LeaderID)

    // Pass token to database queries / transactions
    return executeInvoiceBatch(ctx, token.Epoch)
})
```

---

## 🗄️ Database Optimistic Fencing Pattern

To guarantee strict once-and-only-once execution at the storage layer, store the highest processed epoch in your database table:

```sql
CREATE TABLE job_execution_fence (
    job_name VARCHAR(100) PRIMARY KEY,
    last_processed_epoch BIGINT NOT NULL
);
```

Before processing critical side-effects, execute an optimistic conditional update:

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

    // ... Perform business logic ...
    return tx.Commit()
}
```

---

## 📌 Summary

1. **Context Carrier**: Every `d-cron` job receives a `ctx` populated with the active `FencingToken`.
2. **Monotonic Guarantee**: Epochs strictly increment on every failover, ensuring older leaders can never produce higher tokens than newer leaders.
3. **Storage Fencing**: Combining Go context tokens with database conditional updates guarantees 100% safety even under catastrophic network splits.
