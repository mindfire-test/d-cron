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

Please submit vulnerability reports through one of the following private channels:

1. **GitHub Private Vulnerability Reporting (Recommended)**:
   Open a private advisory report at [https://github.com/mindfire-test/d-cron/security/advisories/new](https://github.com/mindfire-test/d-cron/security/advisories/new).
2. **Security Contact Email**:
   Email details directly to maintainers at `security@mindfiredigital.com`.

### Information to Include

When reporting, please include:

- A description of the vulnerability and its potential impact
- Reproduction steps or minimal reproduction code
- Affected versions
- Suggested mitigation or fix (if available)

Reports are reviewed and addressed promptly by maintainers. For non-security bugs, please use the standard public [issue tracker](https://github.com/mindfire-test/d-cron/issues).

## Security Practices

- **Zero External Daemons**: `d-cron` runs embedded in-process inside your Go application. It opens no external network listener ports and introduces no sidecar daemons, minimizing network attack surface.
- **SQL Injection Prevention**: All internal SQL queries (advisory locking, epoch fencing, history recording, and migrations) use strict parameterized queries (`$1`, `$2`) and strictly validated schema identifiers.
- **Monotonic Split-Brain Fencing**: In distributed topologies, network splits or GC freezes could cause stale nodes to execute overlapping jobs. `d-cron` injects strictly increasing epoch tokens into `context.Context` and provides `dcron.Fence(ctx, tx)` to guarantee transactional write fencing at the database storage layer.
- **Least-Privilege Database Access**: By default, `d-cron` requires zero schema migrations and zero extra database tables. The library operates purely via PostgreSQL session advisory locks, requiring only baseline `CONNECT` privileges on the database.
- **In-Memory Panic Recovery**: Every job executes in an isolated worker goroutine with automatic panic recovery middleware. Uncaught runtime panics in individual job functions are logged with structured stack traces and never terminate the application process or stall the scheduler clock.
- **Bounded Resource Consumption**: Job executions support strict per-job timeouts (`dcron.WithTimeout`), exponential backoff retry caps (`dcron.WithRetry`), and graceful drain bounding (`dcron.WithDrainTimeout`) to prevent database connection exhaustion.

## Responsible Production Configuration

When deploying `d-cron` in production environments:

- **Session-Stable Connections**: Ensure your connection pooler (such as PgBouncer) uses session mode or a direct DSN (`dcron.WithDedicatedLockDSN`) for the advisory lock connection to prevent improper lock pooling across multiplexed connections.
- **Database Transport Encryption**: Always enable TLS (`sslmode=verify-full` or `sslmode=require`) in your PostgreSQL connection string when connecting across public or cloud VPC networks.
