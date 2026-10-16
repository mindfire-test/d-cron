---
id: execution-safety
title: Execution Safety & Panic Recovery
sidebar_label: Execution Safety
---

# 🛡️ Execution Safety & Panic Recovery

Production background job engines must be resilient against runtime panics, unhandled exceptions, and context cancellations. `d-cron` provides built-in safety mechanics to prevent crash cascading.

---

## ⚡ Automatic Panic Recovery

By default, every job routine dispatched by `d-cron` is wrapped in an automatic `recover()` middleware block.

If a job handler triggers a panic (e.g. nil pointer dereference or slice index out of range):

1. **Panic Caught**: The panic value is intercepted immediately.
2. **Stack Trace Logged**: A full structured stack trace is logged via `log/slog` containing job name, instance ID, and error details.
3. **Metrics Incremented**: The `dcron_job_panics_total` Prometheus counter is incremented.
4. **App Resiliency**: The main application and scheduler clock continue operating without interruption.

### Custom Panic Handler

You can supply a custom panic handling callback to forward panics to error monitoring services like Sentry, Bugsnag, or Datadog:

```go
scheduler, err := dcron.New(
    db,
    dcron.WithPanicHandler(func(ctx context.Context, jobName string, err interface{}, stack []byte) {
        // Forward panic to Sentry / Datadog
        sentry.CaptureException(fmt.Errorf("panic in job %s: %v\n%s", jobName, err, string(stack)))
    }),
)
```

---

## ⏱️ Timeout Management & Context Cancellation

Always wrap long-running job logic in deadline-aware contexts. `d-cron` allows configuring per-job timeouts using `dcron.JobOptions`:

```go
scheduler.AddWithOptions("heavy-data-export", "0 3 * * *", 
    func(ctx context.Context) error {
        // ctx will automatically be cancelled after 15 minutes
        req, err := http.NewRequestWithContext(ctx, "POST", "https://api.internal/export", nil)
        if err != nil {
            return err
        }
        _, err = http.DefaultClient.Do(req)
        return err
    },
    dcron.JobOptions{
        Timeout: 15 * time.Minute,
    },
)
```

When a job exceeds its configured timeout:
- `ctx.Done()` is closed with `context.DeadlineExceeded`.
- Long-running HTTP requests, database transactions, or external RPCs associated with `ctx` cancel immediately.
- The job status in history is logged as `TIMED_OUT`.
