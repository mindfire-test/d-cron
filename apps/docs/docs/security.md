---
id: security
title: Security
sidebar_label: Security
---

# Security

`d-cron` takes security, isolation, and data integrity seriously. This page outlines how to report security issues and describes the defensive security practices engineered into the library.

## Reporting Security Vulnerabilities

The `d-cron` team takes the security of our library seriously. If you discover a security vulnerability, please report it responsibly through private channels.

**Do NOT report security vulnerabilities through public GitHub issues, discussions, or pull requests.**

### How to Report Privately

Please submit vulnerability reports through GitHub Private Vulnerability Reporting:

Open a private advisory report at [https://github.com/mindfire-test/d-cron/security/advisories/new](https://github.com/mindfire-test/d-cron/security/advisories/new).

### Information to Include

When reporting, please include:

- A description of the vulnerability and its potential impact
- Reproduction steps or minimal reproduction code
- Affected versions
- Suggested mitigation or fix (if available)

Reports are reviewed and addressed promptly by maintainers. For non-security bugs, please use the standard public [issue tracker](https://github.com/mindfire-test/d-cron/issues).

## Security Practices

- **Zero External Daemons**: `d-cron` runs embedded in-process inside your Go application. It opens no external network listener ports and introduces no sidecar daemons, minimizing network attack surface.
- **SQL Injection Prevention**: All internal SQL queries (advisory locking, epoch fencing, history recording, and migrations) use strict parameterized queries (`$1`, `$2`) and strictly validated schema identifiers. Custom schemas are validated against an identifier allowlist pattern (`^[a-z_][a-z0-9_]{0,62}$`), reserved `"public"` is blocked, and identifiers are escaped with double quotes.
- **Constant-Time Admin Authentication**: When exposing the HTTP Admin API, `dcron.RequireBearerToken(token)` validates credentials using `crypto/subtle.ConstantTimeCompare` to eliminate timing attack vectors. If an empty token is supplied, all requests are safely rejected. An explicit security warning is documented: if `auth` is nil, endpoints are unauthenticated.
- **Automatic Credential Redaction**: `dcron.RedactError(err)` automatically redacts sensitive connection passwords (`user:pass@host`), Bearer tokens (`Bearer ey...`), and key-value secrets (`password=...`, `token=...`) before errors are recorded to hooks or logs.
- **Monotonic Split-Brain Fencing**: In distributed topologies, network splits or GC freezes could cause stale nodes to execute overlapping jobs. `d-cron` injects strictly increasing epoch tokens into `context.Context` and provides `dcron.Fence(ctx, tx)` to guarantee transactional write fencing at the database storage layer.
- **Least-Privilege Database Access**: By default, `d-cron` requires zero schema migrations and zero extra database tables. The library operates purely via PostgreSQL session advisory locks, requiring only baseline `CONNECT` privileges on the database.
- **In-Memory Panic Recovery**: Every job executes in an isolated worker goroutine with automatic panic recovery middleware. Uncaught runtime panics in individual job functions are logged with structured stack traces and never terminate the application process or stall the scheduler clock.
- **Bounded Resource Consumption**: Job executions support strict per-job timeouts (`dcron.WithTimeout`), exponential backoff retry caps (`dcron.WithRetry`), and graceful drain bounding (`dcron.WithDrainTimeout`) to prevent database connection exhaustion.

## Responsible Production Configuration

When deploying `d-cron` in production environments:

- **Session-Stable Connections**: Ensure your connection pooler (such as PgBouncer) uses session mode or a direct DSN (`dcron.WithDedicatedLockDSN`) for the advisory lock connection to prevent improper lock pooling across multiplexed connections.
- **Database Transport Encryption**: Always enable TLS (`sslmode=verify-full` or `sslmode=require`) in your PostgreSQL connection string when connecting across public or cloud VPC networks.
