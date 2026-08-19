// Package elector implements PostgreSQL-advisory-lock based leader election,
// the leadership state machine, and the pooler/keepalive startup gates.
//
// Election runs against a dedicated, session-stable connection to Postgres.
// The leader acquires an advisory lock with pg_try_advisory_lock; while it
// holds the lock it re-confirms ownership with a read-only pg_locks probe --
// it never re-acquires, which would mask a lost lock (SDS §3.5). On shutdown
// the leader explicitly releases the lock before draining (§3.6), and checks
// the unlock return value. The construction gates refuse configurations that
// cannot provide a stable backend for advisory locks: the scheduler refuses to
// start without a session-stability assertion or a dedicated connection (§3.4,
// issue #12) — there is deliberately NO runtime pg_backend_pid() probe — and a
// single-connection pool (§3.4, FR-112). A best-effort TCP-keepalive preflight
// warns when a dead or partitioned leader would hold the lock for hours (§12).
//
// The 4-state machine (UNKNOWN -> LEADER | STANDBY, DEMOTING on the way down)
// emits transitions on a channel, bumps an in-memory epoch fence token on every
// promotion and demotion, and is exercised by unit tests through a
// Backend/Querier seam; no live Postgres is required. The stdlib SQL backend
// (NewStdBackend) is reserved for integration tests once the Phase-0
// testcontainers harness lands. See SDS §3.
package elector
