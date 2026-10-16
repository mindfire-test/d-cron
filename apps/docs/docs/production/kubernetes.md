---
id: kubernetes
title: Kubernetes Deployment Guide
sidebar_label: Kubernetes Deployment
---

# ☸️ Kubernetes Deployment Guide

`d-cron` is built natively for Kubernetes horizontal scaling (`Deployment` with `replicas > 1`).

Because coordination is handled at the database level via PostgreSQL advisory locks, Kubernetes deployments require **no custom CRDs**, **no statefulsets**, and **no RBAC permissions** on the Kubernetes API server.

---

## 📄 Manifest Example: Kubernetes Deployment

Here is a recommended Kubernetes `Deployment` manifest passing the Pod name to `d-cron` as an instance ID:

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
```

---

## 🔧 Wiring Pod Name in Go

Extract `POD_NAME` from environment variables during Go application startup:

```go
package main

import (
	"os"
	"github.com/mindfiredigital/d-cron/dcron"
)

func main() {
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = "local-dev-instance"
	}

	scheduler, err := dcron.New(
		db,
		dcron.WithNamespace("orders-service"),
		dcron.WithInstanceID(podName),
	)
    // ...
}
```

---

## 🔄 Rolling Updates & Graceful Shutdown

During a Kubernetes rolling deployment (`kubectl rollout restart deployment/orders-service`):

1. **SIGTERM Signal**: Kubernetes sends `SIGTERM` to the old active Leader Pod.
2. **Graceful Stop**: `scheduler.Stop()` closes active job contexts and releases the PostgreSQL advisory lock session immediately.
3. **Standby Failover**: One of the remaining pods (or newly launched pod) acquires the advisory lock within `< 1s` and promotes to Leader.

### Shutdown Code Pattern

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

// Wait for termination signal
<-ctx.Done()

log.Println("Shutting down d-cron scheduler...")
scheduler.Stop() // Releases advisory lock session gracefully
```
