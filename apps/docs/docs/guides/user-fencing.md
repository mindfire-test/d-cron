---
id: user-fencing
title: User-Defined Fencing Implementations
sidebar_label: User-Defined Fencing
---

# 🔒 User-Defined Fencing Implementations

Beyond default scheduler protection, distributed applications often need to guard transactional database writes or remote side-effects (e.g. Redis, S3, external payment gateways) against stale leaders delayed by GC pauses or network hiccups.

`d-cron` provides built-in context fencing utilities for this exact purpose.

---

## 🗄️ PostgreSQL Transaction Fencing (`dcron.Fence`)

When `WithHistory` is enabled, `d-cron` updates `{schema}.leader_epoch` whenever a new leader takes over.

Inside any scheduled job handler, call `dcron.Fence(ctx, tx)` inside your database transaction. It verifies under `FOR SHARE` row-level lock that the context's epoch still matches the current active database epoch:

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

## 🔴 Redis Distributed Fencing

When writing to Redis from a scheduled job, use `dcron.Epoch(ctx)` or `dcron.IdempotencyKey(ctx)` as an atomic conditional guard or lock value:

```go
func RedisFencedJob(ctx context.Context, rdb *redis.Client) error {
    epoch := dcron.Epoch(ctx)
    if epoch == 0 {
        return errors.New("missing epoch in context")
    }

    lockKey := "lock:daily-report-generation"

    // Atomic SETNX with epoch token as value
    acquired, err := rdb.SetNX(ctx, lockKey, fmt.Sprintf("epoch_%d", epoch), 5*time.Minute).Result()
    if err != nil {
        return err
    }
    if !acquired {
        return errors.New("concurrent job execution detected in Redis")
    }

    // Execute job logic...
    return nil
}
```

---

## 🪣 Amazon S3 Object Fencing & Idempotency

When generating batch export files to S3, tag object metadata or version filenames with `dcron.IdempotencyKey(ctx)` (e.g. `generate-report:5:1729500000`):

```go
func UploadS3Report(ctx context.Context, s3Client *s3.Client, bucket string, data []byte) error {
    idempotencyKey := dcron.IdempotencyKey(ctx)
    epoch := dcron.Epoch(ctx)

    objectKey := fmt.Sprintf("reports/report_%s.json", idempotencyKey)

    _, err := s3Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket: aws.String(bucket),
        Key:    aws.String(objectKey),
        Body:   bytes.NewReader(data),
        Metadata: map[string]string{
            "x-amz-meta-leader-epoch":   fmt.Sprintf("%d", epoch),
            "x-amz-meta-idempotency-key": idempotencyKey,
        },
    })
    return err
}
```

---

## 📝 Best Practices Checklist

- [x] **Extract epoch with `dcron.Epoch(ctx)`**: Use `dcron.Epoch(ctx)` at the start of transaction-heavy job routines.
- [x] **Call `dcron.Fence(ctx, tx)` in transactions**: Put `dcron.Fence(ctx, tx)` before executing critical mutation queries.
- [x] **Tag side-effects with `dcron.IdempotencyKey(ctx)`**: Attach the idempotency key to external API requests and storage uploads.
