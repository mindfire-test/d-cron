---
id: overlap-missed-runs
title: Overlap & Missed Run Policies
sidebar_label: Overlap & Missed Runs
---

# Overlap & Missed Run Policies

In production environments, a long-running batch job may take longer than its scheduled trigger interval, or an application deployment/restart may cause a scheduled trigger time to be missed.

`d-cron` provides explicit per-job options to configure exact behavior in both scenarios.

## Overlap Policies (`OverlapPolicy`)

When a job scheduled for `*/1 * * * *` (every 1 minute) takes 90 seconds to finish, what should happen when the next 60-second mark arrives?

`d-cron` provides three overlap strategies configured via `dcron.WithOverlapPolicy`:

```go
// Skip overlapping runs (default)
scheduler.Add("sync-user-data", "*/1 * * * *", syncUserHandler,
    dcron.WithNoOverlap(), // Or dcron.WithOverlapPolicy(dcron.OverlapSkip)
)

// Queue a single delayed execution
scheduler.Add("aggregate-metrics", "*/2 * * * *", metricsHandler,
    dcron.WithOverlapPolicy(dcron.OverlapQueue),
)

// Allow concurrent parallel executions
scheduler.Add("scrape-webhooks", "*/5 * * * *", scrapeHandler,
    dcron.WithOverlapPolicy(dcron.OverlapAllow),
)
```

| Policy Constant | Enum Value | Behavior |
| :--- | :--- | :--- |
| `dcron.OverlapSkip` | `0` (Default) | Skips the new execution if a previous instance of the same job is still running. Emits `OutcomeSkipped` to metrics. |
| `dcron.OverlapQueue` | `1` | Queues one pending execution to run immediately after the active instance finishes. |
| `dcron.OverlapAllow` | `2` | Dispatches new executions concurrently in parallel goroutines without waiting. |

## Missed Run Policies (`MissedRunPolicy`)

If your service was offline or restarting between 02:00 AM and 02:15 AM, and a job was scheduled for 02:05 AM, how should `d-cron` respond when the instance boots at 02:15 AM?

```go
scheduler.Add("daily-settlement", "0 2 * * *", settlementHandler,
    // Catch up missed schedules upon restart (requires dcron.WithHistory)
    dcron.WithMissedRunPolicy(dcron.MissedCatchUp),
    dcron.WithMaxLookback(24 * time.Hour), // Only catch up runs missed within last 24h
    dcron.WithMaxCatchUpRuns(5),            // Bound maximum catch-up dispatches
)
```

| Policy Constant | Enum Value | Behavior |
| :--- | :--- | :--- |
| `dcron.MissedSkip` | `0` (Default) | Ignores past missed triggers during downtime and waits for the next future schedule time. |
| `dcron.MissedCatchUp` | `1` | Inspects execution history and runs missed schedules sequentially up to `WithMaxCatchUpRuns` and `WithMaxLookback`. Requires `dcron.WithHistory`. |

### Failover Promotion & Queue Realignment

When a standby replica acquires the advisory lock and is promoted to Leader:
1. **Clock Realignment (`Queue.Realign`)**: The scheduler evaluates the min-heap timer queue against the current system time (`now`).
2. **Stale Schedule Advancement**: For all jobs with `MissedSkip` (the default), any next-run timestamps that fell into the past while the replica was on standby are automatically advanced to the next valid schedule in the future. This prevents thundering bursts of past ticks upon failover.
3. **Catch-Up Processing**: For jobs configured with `MissedCatchUp`, the promoted leader queries the PostgreSQL execution history store, identifies any unexecuted scheduled slots within `WithMaxLookback`, and dispatches them sequentially up to `WithMaxCatchUpRuns`.

## Execution Relative to Last Success (`WithSinceLastSuccess`)

For heavy data syncs or external API scrapers that require a guaranteed quiet period between runs rather than fixed wall-clock alignment:

```go
// Fire 15 minutes AFTER the previous run successfully completed
scheduler.Add("heavy-backup", "@since_success 15m", backupHandler,
    dcron.WithSinceLastSuccess(15 * time.Minute),
)
```

Unlike fixed cron schedules (such as `*/15 * * * *`), `WithSinceLastSuccess`:
- Calculates the next fire time relative to `finished_at` of the previous successful run.
- Ensures a long-running batch job taking 45 minutes does not trigger immediately upon finishing; it rests for the full 15-minute interval before running again.

## Overlap Handling During Retries

When a job is configured with exponential backoff retries (`dcron.WithRetry`):
- `WithNoOverlap()` (`OverlapSkip`) spans the entire logical execution lifecycle, including all retry backoff delays.
- If a job fails attempt 1 and is sleeping for a 30-second backoff when the next regular cron tick arrives, `d-cron` recognizes the active logical execution and skips the scheduled tick.
- This guarantees that retrying jobs never collide with regular scheduled ticks.

## Example Configuration Matrix

```go
// 1. Financial Ledger Audit (Queue overlap, catch up missed runs, 30m timeout)
scheduler.Add("ledger-audit", "0 0 * * *", auditFn,
    dcron.WithOverlapPolicy(dcron.OverlapQueue),
    dcron.WithMissedRunPolicy(dcron.MissedCatchUp),
    dcron.WithMaxLookback(12 * time.Hour),
    dcron.WithTimeout(30 * time.Minute),
)

// 2. Realtime Cache Refresh (Skip overlaps, skip missed runs, no retries, 2m timeout)
scheduler.Add("cache-warmup", "*/5 * * * *", warmupFn,
    dcron.WithNoOverlap(),
    dcron.WithMissedRunPolicy(dcron.MissedSkip),
    dcron.WithNoRetry(),
    dcron.WithTimeout(2 * time.Minute),
)

// 3. Database Compaction (Guaranteed 1-hour quiet period after success)
scheduler.Add("db-compaction", "@since_success 1h", compactFn,
    dcron.WithSinceLastSuccess(1 * time.Hour),
    dcron.WithTimeout(45 * time.Minute),
)
```
