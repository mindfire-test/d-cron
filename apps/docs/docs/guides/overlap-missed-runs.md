---
id: overlap-missed-runs
title: Overlap & Missed Run Policies
sidebar_label: Overlap & Missed Runs
---

# 🔄 Overlap & Missed Run Policies

In real-world production environments, job executions might run longer than their scheduled trigger interval, or an application deployment/restart might cause a scheduled trigger time to be missed.

`d-cron` provides explicit policies to configure exact behavior in both scenarios.

---

## 🔀 Overlap Policies (`OverlapPolicy`)

When a job scheduled for `@every 1m` takes 90 seconds to finish, what should happen when the 60-second mark arrives?

`d-cron` supports 4 overlap policies:

```go
scheduler.AddWithOptions("sync-user-data", "*/1 * * * *", 
    syncUserHandler,
    dcron.JobOptions{
        OverlapPolicy: dcron.OverlapSkip, // Default
    },
)
```

| Policy | Enum Constant | Behavior |
| :--- | :--- | :--- |
| **Skip** (Default) | `dcron.OverlapSkip` | Skips the new execution if a previous instance of the same job is still running. Logs a skipped metric. |
| **Concurrent** | `dcron.OverlapConcurrent` | Allows multiple instances of the job to execute concurrently in parallel goroutines. |
| **Queue** | `dcron.OverlapQueue` | Queues the new execution in a buffer. Runs immediately when the current instance completes. |
| **Cancel Previous** | `dcron.OverlapCancelPrevious` | Cancels the active job context (`ctx.Cancel()`) and immediately launches the new execution instance. |

---

## ⏰ Missed Run Policies (`MissedRunPolicy`)

If your service was offline or restarting between 02:00 AM and 02:15 AM, and a job was scheduled for 02:05 AM, how should `d-cron` respond when the instance boots at 02:15 AM?

```go
scheduler.AddWithOptions("daily-settlement", "0 2 * * *", 
    settlementHandler,
    dcron.JobOptions{
        MissedRunPolicy: dcron.MissedRunRunLatest,
    },
)
```

| Policy | Enum Constant | Behavior |
| :--- | :--- | :--- |
| **Skip Missed** (Default) | `dcron.MissedRunSkip` | Ignores past missed triggers and waits for the next future schedule time. |
| **Run Latest** | `dcron.MissedRunRunLatest` | Detects that 1 or more runs were missed during downtime and executes **exactly 1 run** immediately upon startup. |
| **Catch Up** | `dcron.MissedRunCatchUp` | Executes **all** missed run intervals sequentially until caught up to current clock time. |

---

## 💡 Example Configuration Matrix

```go
// Example: Financial Ledger Audit (Never skip, run latest missed run)
scheduler.AddWithOptions("ledger-audit", "0 0 * * *", auditFn, dcron.JobOptions{
    OverlapPolicy:   dcron.OverlapQueue,
    MissedRunPolicy: dcron.MissedRunRunLatest,
    Timeout:         30 * time.Minute,
})

// Example: Realtime Cache Refresh (Safe to skip overlap and missed runs)
scheduler.AddWithOptions("cache-warmup", "*/5 * * * *", warmupFn, dcron.JobOptions{
    OverlapPolicy:   dcron.OverlapSkip,
    MissedRunPolicy: dcron.MissedRunSkip,
    Timeout:         2 * time.Minute,
})
```
