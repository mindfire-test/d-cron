---
id: user-fencing
title: User-Defined Fencing Implementations
sidebar_label: User-Defined Fencing
---

# 🔒 User-Defined Fencing Implementations

Beyond SQL optimistic epoch locking, developers can apply fencing tokens to external systems like Redis, S3, or third-party webhooks.

---

## 🔴 Redis Distributed Lock Fencing

When writing output data to Redis from a scheduled job, use the `FencingToken.Epoch` as a conditional guard or lock value:

```go
func RedisFencedJob(ctx context.Context, rdb *redis.Client) error {
    token, ok := dcron.GetFencingToken(ctx)
    if !ok {
        return errors.New("missing fencing token")
    }

    // Key format: lock:job_name
    lockKey := "lock:daily-report-generation"
    
    // Set lock key with epoch token as value
    // SET lock:daily-report-generation epoch_14 NX EX 300
    acquired, err := rdb.SetNX(ctx, lockKey, fmt.Sprintf("epoch_%d", token.Epoch), 5*time.Minute).Result()
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

## 🪣 Amazon S3 Object Fencing

When producing scheduled report files to S3, include the leader epoch token in object metadata or object key versioning to avoid overwriting newer reports:

```go
func UploadS3Report(ctx context.Context, s3Client *s3.Client, bucket string, data []byte) error {
    token, _ := dcron.GetFencingToken(ctx)

    objectKey := fmt.Sprintf("reports/2026-09-21/report_epoch_%d.json", token.Epoch)

    _, err := s3Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket: aws.String(bucket),
        Key:    aws.String(objectKey),
        Body:   bytes.NewReader(data),
        Metadata: map[string]string{
            "x-amz-meta-leader-epoch": fmt.Sprintf("%d", token.Epoch),
            "x-amz-meta-leader-id":    token.LeaderID,
        },
    })
    return err
}
```

---

## 📝 Best Practices Checklist

- [x] **Always check context**: Verify `dcron.GetFencingToken(ctx)` at the start of job handlers.
- [x] **Pass epoch down**: Include `Epoch` in database transaction updates or API call headers.
- [x] **Handle abort gracefully**: If a fencing check fails, return `nil` or log warning without raising critical system alerts.
