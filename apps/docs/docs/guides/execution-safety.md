---
id: execution-safety
title: Execution Safety, Panic Recovery & Retries
sidebar_label: Execution Safety & Retries
---

# Execution Safety, Panic Recovery & Retries

Production background job engines must be resilient against runtime panics, unhandled errors, and external timeouts. `d-cron` provides built-in safety mechanics to prevent crash cascading and ensure reliable task execution.

## Automatic Panic Recovery

Every job routine dispatched by `d-cron` is isolated in its own goroutine wrapped with an automatic `recover()` block.

If a job handler panics (e.g. nil pointer dereference or index out of range):

1. **Panic Caught**: The panic is intercepted immediately without terminating the process.
2. **Stack Trace Logged**: A full structured stack trace is logged via `log/slog` with job name and instance ID.
3. **Metrics Emitted**: Emits `metrics.OutcomePanicked` to `JobFinished` on the configured `metrics.Recorder`.
4. **Hooks Notified**: Terminal hooks receive the panic value and stack bytes for external alerting.
5. **Scheduler Continues**: The leader election coordinator and scheduling clock continue operating without interruption.

### Alerting on Panics via Notification Hooks

Register notification hooks using `dcron.WithHooks`:

```go
// Custom alerting hook (e.g. Sentry / Datadog)
alertHook := dcron.HookFunc(func(ctx context.Context, res dcron.Result) error {
    if res.Outcome == dcron.OutcomePanicked {
        log.Printf("ALERT: Job %s panicked: %v (after %d attempts in %v)", 
            res.Name, dcron.RedactError(res.Error), res.Attempts, res.Duration)
    }
    return nil
})

scheduler, err := dcron.New(
    db,
    dcron.WithHooks(alertHook),
)
```

## Exponential Backoff, Jitter Retries & Retry Filters

Transient errors (e.g. database deadlocks, network timeouts) can be retried automatically using `dcron.WithRetry`:

```go
scheduler.Add("charge-pending-subscriptions", "0 6 * * *", chargeFn,
    dcron.WithRetry(dcron.Retry{
        Attempts:   3,                 // Attempt up to 3 times (1 initial run + 2 retries)
        Backoff:    500 * time.Millisecond, // Initial delay
        Factor:     2.0,               // Double delay each retry (500ms, 1s, 2s)
        MaxBackoff: 10 * time.Second,  // Cap maximum backoff delay
        Jitter:     true,              // Randomize delay to prevent synchronized retry spikes
        Timeout:    30 * time.Second,  // Timeout for each individual attempt
        Retryable: func(err error) bool {
            // Only retry transient errors; abort immediately on terminal business validation
            return !errors.Is(err, ErrInvalidCustomerAccount)
        },
    }),
)
```

### Disabling Retries (`dcron.WithNoRetry`)

For non-idempotent operations where re-executing on failure could cause duplicate side effects (e.g., initiating wire transfers or sending customer SMS messages), explicitly disable retries using `dcron.WithNoRetry()`:

```go
scheduler.Add("send-welcome-sms", "*/10 * * * *", sendSMSFn,
    dcron.WithNoRetry(),
)
```

> **Note on Default Retry Attempts**: If `WithRetry` is omitted, `d-cron` defaults to 5 attempts for backward compatibility. Use `WithNoRetry()` whenever an operation must execute at most once.

### Sanitizing Error Messages (`dcron.RedactError`)

To prevent accidental leakage of database credentials, bearer tokens, or API keys into application logs, monitoring alerts, or webhook payloads, wrap error strings with `dcron.RedactError(err)`:

```go
sanitizedMsg := dcron.RedactError(err)
```

## Timeout Management & Context Cancellation

Wrap long-running jobs with explicit deadlines using `dcron.WithTimeout`:

```go
scheduler.Add("heavy-data-export", "0 3 * * *", 
    func(ctx context.Context) error {
        // ctx will be cancelled automatically after 15 minutes
        req, err := http.NewRequestWithContext(ctx, "POST", "https://api.internal/export", nil)
        if err != nil {
            return err
        }
        _, err = http.DefaultClient.Do(req)
        return err
    },
    dcron.WithTimeout(15 * time.Minute),
)
```

When a job exceeds its configured timeout:
- `ctx.Done()` is closed with `context.DeadlineExceeded`.
- In-flight database queries or HTTP requests carrying `ctx` abort immediately.
- Outcome `OutcomeTimedOut` is recorded in metrics and execution history.

## Graceful Draining on Shutdown

When `scheduler.Stop(ctx)` is called (e.g. on `SIGTERM`):
1. The leader releases the advisory lock cleanly.
2. In-flight jobs are allowed to complete up to `WithDrainTimeout(d)` (default: 30 seconds).
3. If jobs exceed the drain timeout, their contexts are cancelled.
