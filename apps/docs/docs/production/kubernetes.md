---
id: kubernetes
title: Production & Kubernetes
sidebar_label: Production & Kubernetes
---

# Production Deployment & Database Tuning

`d-cron` is built natively for horizontal scaling in containerized production environments. Because coordination occurs at the database layer via PostgreSQL advisory locks, deployments require **no custom Kubernetes CRDs**, **no statefulsets**, and **no RBAC permissions**.

This guide covers production deployment manifests, readiness probes, PgBouncer pooler setup, and `pgx/v5` driver tuning.

## 1. Kubernetes Deployment Manifest

Here is a recommended Kubernetes `Deployment` manifest passing the Pod name to `d-cron` via the Downward API:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-service
  namespace: production
  labels:
    app: orders-service
spec:
  replicas: 5
  selector:
    matchLabels:
      app: orders-service
  template:
    metadata:
      labels:
        app: orders-service
    spec:
      containers:
        - name: orders-service
          image: registry.internal/orders-service:v1.4.0
          env:
            # Inject Pod Name via Downward API
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: db-credentials
                  key: url
          ports:
            - containerPort: 8080
              name: http
            - containerPort: 9090
              name: metrics
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 15
            periodSeconds: 20
          lifecycle:
            preStop:
              exec:
                command: ["/bin/sleep", "5"]
```

## 2. Health & Readiness Probes

In your Go application, use `scheduler.HealthCheck(ctx)` to verify that both the database pool and the dedicated advisory lock connection are reachable:

```go
http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    // Both Leader and Standby pods are healthy as long as connectivity is alive!
    if err := scheduler.HealthCheck(r.Context()); err != nil {
        w.WriteHeader(http.StatusServiceUnavailable)
        fmt.Fprintf(w, "health check failed: %v", err)
        return
    }
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
})
```

> **Important**: Do not fail readiness probes on Standby pods! Standby pods are healthy and ready to serve user HTTP requests, background API queries, and take over leadership instantly if the active leader terminates.

## 3. Delivery Guarantees

Under the d-cron single-leader coordination architecture:
- **Normal Operation**: **At most once per scheduled tick**. Exactly one elected leader pod evaluates schedules and dispatches job functions.
- **Failover with Catch-Up**: **At least once with catch-up enabled** (`MissedCatchUp`). If a leader fails during execution, a newly promoted standby catches up missed ticks up to `WithMaxCatchUpRuns` and `WithMaxLookback`.
- **Split-Brain Mitigation**: Every job receives `dcron.Epoch(ctx)` and `dcron.IdempotencyKey(ctx)`. Using `dcron.Fence(ctx, tx)` ensures older leaders cannot commit writes after promotion of a new leader.

## 4. PgBouncer and Connection Pooling

PostgreSQL advisory locks are **Session-Bound**, meaning they attach to the physical TCP connection between client and database.

### The Transaction Pooling Trap

When PgBouncer runs in **Transaction Pooling Mode** (`pool_mode = transaction`), physical server connections are reassigned after every transaction. Session advisory locks will fail or be lost when the connection is recycled!

### Solution A: Dedicated Direct Lock DSN (Recommended)

Let normal application queries flow through PgBouncer while configuring `d-cron` to open an independent, dedicated connection directly to PostgreSQL for the advisory lock:

```go
// 1. Normal application queries use PgBouncer in transaction mode
appDB, _ := sql.Open("postgres", "postgres://user:pass@pgbouncer:6432/myapp_db?sslmode=disable")

// 2. d-cron leader lock connects directly to PostgreSQL (port 5432)
scheduler, _ := dcron.New(
    appDB,
    dcron.WithNamespace("orders-service"),
    dcron.WithDedicatedLockDSN("postgres://user:pass@postgres-direct:5432/myapp_db?sslmode=disable"),
)
```

If your project registers a custom driver name (such as `"pgx"`), use `dcron.WithDedicatedLockDriver`:

```go
scheduler, _ := dcron.New(
    appDB,
    dcron.WithNamespace("orders-service"),
    dcron.WithDedicatedLockDriver("pgx", "postgres://user:pass@postgres-direct:5432/myapp_db?sslmode=disable"),
)
```

### Solution B: PgBouncer Session Pool Mode

If all connections must flow through PgBouncer, create a dedicated PgBouncer database alias in **Session Pooling Mode**:

```ini
[databases]
; Normal app connection (transaction mode)
myapp_db = host=postgres-db port=5432 dbname=myapp_db pool_mode=transaction

; Dedicated d-cron alias (session mode)
myapp_cron_db = host=postgres-db port=5432 dbname=myapp_db pool_mode=session
```

Then in Go:

```go
lockDB, _ := sql.Open("postgres", "postgres://user:pass@pgbouncer:6432/myapp_cron_db?sslmode=disable")
scheduler, _ := dcron.New(
    lockDB,
    dcron.WithNamespace("orders-service"),
    dcron.WithSessionStableConnection(),
)
```

## 5. Driver Setup: `pgx/v5` & `lib/pq`

`d-cron` supports any standard `*sql.DB` connection pool (`lib/pq`, `pgx/v5/stdlib`).

### Setup with `pgx/v5/stdlib`

```go
package main

import (
	"context"
	"database/sql"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/mindfire-test/d-cron/dcron"
)

func main() {
	dsn := "postgres://user:pass@localhost:5432/myapp_db?sslmode=disable"
	
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("failed to open postgres: %v", err)
	}

	// Recommended connection pool settings for d-cron DB handle
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0) // Do not forcibly close session connection

	scheduler, err := dcron.New(
		db,
		dcron.WithNamespace("my-service"),
		dcron.WithSessionStableConnection(),
	)
	if err != nil {
		log.Fatalf("failed to init scheduler: %v", err)
	}

	scheduler.Start(context.Background())
}
```

### Setup with `lib/pq`

```go
import (
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/mindfire-test/d-cron/dcron"
)

db, _ := sql.Open("postgres", "postgres://user:pass@localhost:5432/myapp?sslmode=disable")
scheduler, _ := dcron.New(
	db, 
	dcron.WithNamespace("my-service"),
	dcron.WithSessionStableConnection(),
)
```

## 6. Graceful Shutdown & Drain Timeout

Always listen for OS termination signals (`SIGTERM`, `SIGINT`) to allow running jobs to finish and release the leadership lock gracefully:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

// Start scheduler in background
scheduler.Start(context.Background())

<-ctx.Done()
log.Println("Shutting down d-cron scheduler...")

// Stop drains active jobs up to configured DrainTimeout (default: 30s)
drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := scheduler.Stop(drainCtx); err != nil {
    log.Printf("Error stopping scheduler: %v", err)
}
```
