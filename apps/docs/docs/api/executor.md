---
id: executor
title: Job Types & Options
sidebar_label: Job & Executor Types
---

# 🛠️ Job Types & Options Reference

Detailed reference of job handler signatures, execution options, and enum types in `d-cron`.

---

## 📝 `JobFunc` Signature

```go
type JobFunc func(ctx context.Context) error
```

Every job handler registered with `d-cron` must match this signature. The provided `ctx` carries cancellation signals and fencing tokens.

---

## ⚙️ `JobOptions` Struct

```go
type JobOptions struct {
    OverlapPolicy   OverlapPolicy   // Overlap handling strategy
    MissedRunPolicy MissedRunPolicy // Downtime catchup strategy
    Timeout         time.Duration   // Maximum execution duration (0 = unlimited)
}
```

---

## 🔀 `OverlapPolicy` Enum

```go
type OverlapPolicy int

const (
    OverlapSkip           OverlapPolicy = iota // Skip execution if previous instance is still running
    OverlapConcurrent                          // Execute concurrently in parallel
    OverlapQueue                               // Queue execution in buffer
    OverlapCancelPrevious                      // Cancel active job context and re-run
)
```

---

## ⏰ `MissedRunPolicy` Enum

```go
type MissedRunPolicy int

const (
    MissedRunSkip      MissedRunPolicy = iota // Ignore missed schedules during downtime
    MissedRunRunLatest                        // Run exactly 1 missed schedule upon startup
    MissedRunCatchUp                          // Run all missed schedules sequentially
)
```

---

## 🛡️ `FencingToken` Struct

```go
type FencingToken struct {
    Epoch    int64  // Monotonically increasing election epoch counter
    LeaderID string // Node instance ID string
}
```
