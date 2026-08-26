# `d-cron`

## 1. Executive Summary

As backend applications written in Go scale horizontally across `N` container replicas, standard in-process cron libraries like `robfig/cron` fail. Because each replica runs an independent scheduler instance, a job scheduled for "daily at 2:00 AM" fires **`N` times simultaneously**.

Existing solutions in the ecosystem present major tradeoffs:
- **Task Queues (`asynq`, `river`)**: Heavy producer-consumer architectures requiring custom queue tables, database migrations.
- **Per-Job Lock Racing (`gocron`)**: Spikes database CPU and connection pools at exact minute boundaries, also lack split-brain safety.

---

## 2. Prior Art & Ecosystem Research

A technical audit of existing scheduling tools was conducted to verify industry gaps:

### A. `robfig/cron`
- **Mechanics**: In-memory min-heap timer queue in a single Go process.
- **Limitation**: Zero cross-replica awareness. In `N` replicas, jobs execute `N` times.

### B. `hibiken/asynq`
- **Mechanics**: Redis-backed async task queue with a `Scheduler` component.
- **Limitation**: No Leader Election. Running `N` schedulers pushes duplicate tasks into Redis. Asynq's official guidance is to either run a single dedicated scheduler pod (Single Point of Failure) or implement manual Redis task-uniqueness windows.

### C. `go-co-op/gocron`
- **Mechanics**: In-process scheduler with an optional `gocron-redis-lock` plugin.
- **Limitation**: Uses per-job lock racing, Requires Redis, + no Leader Election.

### D. `Quartz Scheduler`
- **Mechanics**: JDBC-backed job store using database row-level locking for leader election.
- **Takeaway**: For Java-only.

---

## 3. Competiters

| Factor | `libi/dcron` | `Dkron` | `riverqueue/river` | **`d-cron` (Our Tool)** |
| :--- | :--- | :--- | :--- | :--- |
| **System Architecture** | Go Library (Redis/etcd) | Standalone Server Cluster | Postgres Job Queue System | In-Process |
| **Infra Required** | Redis or etcd | Dkron Server Daemon Cluster | Postgres + DB Queue Tables | Postgres |
| **Execution Model** | In-Process Go | External HTTP/Shell/Docker | Worker Queue Deserialization | In-Process Go |
| **Leader Election** | Per-Job Lock | Raft Consensus | `river_leader` Table | Postgres Advisory Lock |
| **Split-Brain Fencing**| NA | NA | NA | Monotonic Epoch Tokens |
| **Cost** | Open Source | Pro: **$450 / year** | Durable Cron requires Pro | Open Source |

### Why `d-cron` is Superior for Postgres Go Stacks:
1. **vs. `river`**: River is a heavy producer-consumer task queue requiring database migrations and queue tables `d-cron` provides free, table-less distributed cron in open-source.
2. **vs. `libi/dcron`**: `libi/dcron` forces Redis/etcd driver dependencies (`dcron-contrib`). `d-cron` is Postgres-native.
3. **vs. `Dkron`**: `Dkron` is a separate cluster daemon. `d-cron` is a single Go import.

---

## 4. The Distributed Scheduling Methods

| # | Method | How It Operates |
| :-: | :--- | :--- |
| **1** | **Leader Election** | 1 active Leader server runs clock; $N-1$ standbys poll |
| **2** | **Per-Job Lock Race** | All servers wake up at trigger time & race for locks |
| **3** | **Task Queue** | Central scheduler enqueues jobs to queues |
| **4** | **Consistent Hash Ring**| Jobs are partitioned across server segments |
| **5** | **Ephemeral Orchestrator**| Cloud/K8s master spawns new container per run |
| **6** | **Static Modulo** | Legacy hardcoded assignment (`job_id % num_nodes`) |

**Leader Election** is selected for `d-cron` because it completely eliminates the Thundering Herd problem while requiring **zero new infrastructure**.

---

## 5. Core Problems & Solutions

### A. Leader Election & PostgreSQL Advisory Locks
- **Problem**: How to elect 1 leader across replicas with zero new database.
- **Solution**: Utilizing PostgreSQL advisory locks (`pg_try_advisory_lock`). 

### B. Split-Brain Protection & Monotonic Fencing Tokens
- **Problem**: If Leader 1 experiences a 10-second pause, Standby 2 becomes Leader 2. When Leader 1 unfreezes, both nodes think they are Leader (Split-Brain).
- **Solution**: Maintaining a strictly increasing **Monotonic Leader Epoch Counter** (`Epoch 1 -> Epoch 2`). This token is injected into job execution contexts (`context.Context`). Database writes check `Execution.LeaderEpoch`; stale writes from demoted leaders (`Epoch 1`) are immediately rejected.

### C. Failure Handling & Execution Safety
- **Panic Protection**: `executor` wrapper captures panics (`recover()`), logs full stack traces, and prevents job panics from crashing the main Go server.
- **Retry Policy**: Exponential backoff retry policies for failed job executions.

### D. Observability & Dashboard
- **Embedded Web UI**: Single HTTP handler mounted on the application router rendering active leader status, registered jobs, last run duration, and execution history.
- **Prometheus Metrics**: Exporting required metrics

---

## 6. Conclusion

`d-cron` fills an unaddressed gap in the Go software ecosystem by utilizing PostgreSQL advisory locks to provide safe distributed cron execution using the existing database.