---
id: faq
title: Frequently Asked Questions (FAQ)
sidebar_label: FAQ
---

# ❓ Frequently Asked Questions (FAQ)

---

### Q: Why build `d-cron` instead of using existing libraries like `gocron` or `asynq`?

**A:** Standard in-process cron libraries like `robfig/cron` or `gocron` do not natively coordinate single-leader election across multiple scaled pod replicas, resulting in duplicate job runs or database connection lock spikes at trigger boundaries.

Task queues like `asynq` or `river` solve coordination but require introducing extra infrastructure (Redis or custom PostgreSQL job payload queue tables with schema migrations). `d-cron` provides safe distributed single-leader coordination **in-process using PostgreSQL advisory locks with zero new database tables.**

---

### Q: Does `d-cron` create any database tables by default?

**A:** **No.** By default, `d-cron` operates purely using PostgreSQL Session Advisory Locks in Postgres memory structures (`pg_try_advisory_lock`). It requires zero DDL schema migrations. 

Tables are only created if you explicitly enable optional execution history (`dcron.WithHistory(retention)`).

---

### Q: What happens if the active Leader node crashes or loses network connectivity?

**A:** The TCP session between the Leader node and PostgreSQL terminates. PostgreSQL automatically drops the session-bound advisory lock. Within the configured polling interval (default: 5 seconds with jitter), one of the remaining Standby nodes acquires the advisory lock, increments the monotonic leader epoch, and takes over scheduling seamlessly.

---

### Q: Does `d-cron` work with MySQL, Redis, or etcd?

**A:** PostgreSQL is the native out-of-the-box backend using 64-bit session advisory locks. However, `d-cron` defines a clean `Backend` interface (`internal/elector.Backend`) allowing custom backend adapters or test doubles.

---

### Q: Is `d-cron` compatible with PgBouncer?

**A:** Yes! Because advisory locks are session-bound, you can either configure `dcron.WithDedicatedLockDSN` to point directly to PostgreSQL (bypassing PgBouncer for the lock connection), or configure a dedicated PgBouncer database alias in `session` pool mode and pass `dcron.WithSessionStableConnection()`. See the [PgBouncer Setup Guide](./production/pgbouncer.md) for full details.

---

### Q: Can I run high-frequency jobs (e.g. every 1 second)?

**A:** Yes. Pass `dcron.WithSecondsField()` to enable 6-field cron expressions with second-level precision. Sub-millisecond internal scheduling overhead makes high-frequency cron schedules fast and efficient.

---

### Q: How do I contribute or report bugs?

**A:** `d-cron` is 100% open source under the MIT license! Contributions, issues, and feature requests are welcome on [GitHub](https://github.com/mindfire-test/d-cron).
