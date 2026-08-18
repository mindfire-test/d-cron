// Package elector implements PostgreSQL-advisory-lock based leader election,
// the leadership state machine, and pooler/session-stability detection.
//
// Election runs against a dedicated, session-stable connection to Postgres.
// The leader acquires an advisory lock with pg_try_advisory_lock; while it
// holds the lock it re-confirms ownership with a read-only pg_locks probe --
// it never re-acquires, which would mask a lost lock (SDS §3.5). On shutdown
// the leader explicitly releases the lock (§3.3). The session-stability and
// single-connection gates at construction refuse configurations (a
// transaction-mode pooler, or MaxOpenConnections==1) that cannot provide a
// stable backend for advisory locks (§3.4, FR-108/FR-112).
//
// The lock lifecycle and state machine are fully implemented here and
// exercised by unit tests through a Backend/Querier seam; no live Postgres is
// required to test the logic. The stdlib SQL backend (NewStdBackend) is
// reserved for integration tests once the Phase-0 testcontainers harness lands.
// See SDS §3.
package elector
