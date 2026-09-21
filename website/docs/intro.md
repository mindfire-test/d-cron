---
id: intro
title: Introduction to d-cron
sidebar_label: Overview & Motivation
---

# `d-cron`: Distributed Cron Scheduler for Go

**`d-cron`** is a high-performance, embeddable Go library that provides safe, coordinated cron-style job scheduling across an arbitrary number of application container replicas — **using only the PostgreSQL database your application already operates.**

```
robfig/cron ────► d-cron ────────► River / asynq ──► Dkron ──────► Temporal
in-process        coordinated      task queue        standalone    workflow
cron only         cron (this)      + cron            cluster       engine
```

---

## 💡 The Problem

Standard in-process cron libraries (such as `robfig/cron`) maintain an in-memory timer heap scoped strictly to a single Go process.

When your application scales horizontally to `N` container replicas in Kubernetes or Docker:
- Each replica independently reaches the trigger boundary.
- A job scheduled for `0 2 * * *` (2:00 AM) **fires `N` times simultaneously**.

Existing workarounds introduce heavy architectural friction:
- **Task Queues (`asynq`, `river`)**: Heavy producer-consumer architectures requiring custom queue tables, database migrations, or Redis clusters.
- **Per-Job Lock Racing (`gocron`)**: Spikes database CPU and connection pools at exact minute boundaries with zero split-brain safety.
- **External Daemons (`Dkron`)**: Requires deploying, managing, and monitoring an additional external cluster daemon.

---

## 🚀 Key Advantages of `d-cron`

1. **Zero New Infrastructure**: Uses PostgreSQL session-bound advisory locks (`pg_try_advisory_lock`). No Redis, no etcd, no sidecars.
2. **Single Leader Scheduler**: 1 active Leader replica runs the timer clock; **0 thundering-herd database lock spikes** at trigger boundaries.
3. **Split-Brain Fencing**: Injects monotonic leader epoch tokens (`LeaderEpoch`) into job `context.Context` to fence stale database writes if a network partition occurs.
4. **Zero Migration Default**: By default, `d-cron` creates **zero database tables** and requires zero schema migrations.
5. **In-Process Go Functions**: Register standard Go functions directly (`scheduler.Add`). No JSON payload serialization required.
6. **Built-in Observability**: Optional embedded web dashboard UI, Prometheus metrics adapter, OpenTelemetry tracing, and HTTP admin API.

---

## 📊 Comparison Matrix

| Factor | `robfig/cron` | `gocron` | `asynq` | `river` | **`d-cron`** |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Architecture** | In-Process | In-Process | Task Queue | Task Queue | **In-Process Library** |
| **Infra Required** | None | Redis | Redis | Postgres + Tables | **Postgres (Zero extra tables)** |
| **Execution Model** | Direct Go | Direct Go | Async Task Worker | Async Task Worker | **Direct In-Process Go** |
| **Coordination** | None | Per-Job Lock Race | Queue Enqueue | Leader Election | **Single Leader Election** |
| **Split-Brain Fencing** | ❌ No | ❌ No | ❌ No | ❌ No | **v Monotonic Epoch Tokens** |
| **Open Source** | Open Source | Open Source | Open Source | Paid / Pro Features | **100% Open Source** |

---

## ⚡ Next Steps

- Check out the [Quickstart Guide](./quickstart.md) to integrate `d-cron` into your Go application in 2 minutes.
- Read about [PostgreSQL Advisory Lock Architecture](./architecture/advisory-locks.md) to understand how single-leader election works under the hood.
