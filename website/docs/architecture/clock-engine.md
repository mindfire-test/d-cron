---
id: clock-engine
title: In-Memory Clock Engine
sidebar_label: Clock Engine
---

# ⏱️ In-Memory Clock Engine

`d-cron` features a high-precision, low-overhead in-memory timer engine derived from robust cron parsing semantics (`robfig/cron/v3`).

---

## 🏗️ Architecture & Timer Wheel

When an instance becomes **Leader**, `d-cron` initializes an internal min-heap timer wheel containing all registered cron entries:

```
                  ┌───────────────────────────────┐
                  │      Leader Timer Engine      │
                  └──────────────┬────────────────┘
                                 │ Next Trigger Evaluation
                                 ▼
              ┌──────────────────────────────────────┐
              │  Sorted Min-Heap (Next Run Timestamps)│
              │  1. 10:00:00 - billing-sync          │
              │  2. 10:05:00 - telemetry-flush       │
              │  3. 10:15:00 - cache-prune           │
              └──────────────────┬───────────────────┘
                                 │ Sleep until 10:00:00
                                 ▼
                     ┌───────────────────────┐
                     │ Worker Goroutine Pool │
                     └───────────────────────┘
```

### Key Clock Engine Features

1. **Zero Polling Drift**: Rather than waking up every second to query PostgreSQL or iterate over slices, `d-cron` calculates exact sleep durations until the next earliest job trigger time.
2. **Standard 5-Field & 6-Field Cron Expressions**: Supports standard 5-field syntax (`0 * * * *`), optional second-level resolution (`0 0 12 * * *`), and specifiers (`@every 5m`, `@hourly`, `@daily`).
3. **Timezone Support**: Full support for `CRON_TZ` headers and location specifiers:
   ```go
   // Runs every day at 9:00 AM New York time
   scheduler.Add("ny-morning-report", "CRON_TZ=America/New_York 0 9 * * *", jobFn)
   ```

---

## ⚡ Concurrency & Worker Pools

When a job's scheduled time arrives, `d-cron` does **not** block the main timer loop. Instead, it dispatches execution to an internal async worker pool:

```
Timer Trigger ---> Acquire Worker Token ---> Launch Job Goroutine ---> Track Metrics
```

- **Isolated Goroutines**: Each job runs in its own spawned goroutine with panic recovery middleware.
- **Panic Protection**: A panic in one job handler is caught, logged with stack trace to `log/slog`, and does not crash the application or stop the timer clock.

---

## 📊 Clock Engine Benchmarks

Performance metrics on standard AMD64 hardware (8-core CPU):

| Metric | Result |
| :--- | :--- |
| **Heap Insertion / Reschedule** | `< 250 ns` per job |
| **Memory per 1,000 Registered Jobs** | `~ 1.2 MB` |
| **Trigger Precision** | `< 1 ms` variance under default OS scheduler |
