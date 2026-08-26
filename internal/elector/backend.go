package elector

import (
	"context"
	"database/sql"
	"fmt"
)

// SQL statements issued against the dedicated lock connection. pg_try_advisory_lock
// and pg_advisory_unlock use the "big lock" (single int8) form; the key is
// derived from the namespace by LockKey.
const (
	sqlPID     = `SELECT pg_backend_pid()`
	sqlTryLock = `SELECT pg_try_advisory_lock($1), pg_backend_pid()`
	// pg_locks has no key1/key2 columns; the single-bigint advisory form is
	// stored with classid = high 32 bits, objid = low 32 bits of the key
	// (PostgreSQL "Advisory Locks", one-key form; verified against PG 16).
	// Both columns are SIGNED int4, so a half with bit 31 set is stored as a
	// negative int4. Masking every side to 32-bit unsigned before comparing
	// makes the probe immune to the sign extension — an exact bigint compare
	// silently never matches when either half is negative, which made
	// HoldsLock report "not held" on the owning session and the leader
	// demote on every poll (failover deadlock found under issue #28).
	sqlHolds = `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory'
		AND (classid::bigint & 4294967295) = (($1::bigint >> 32) & 4294967295)
		AND (objid::bigint & 4294967295) = ($1::bigint & 4294967295)
		AND pid = pg_backend_pid())`
	sqlRelease = `SELECT pg_advisory_unlock($1)`
)

// Backend is the database seam behind the elector. The production
// implementation wraps a dedicated, session-stable *sql.Conn (NewStdBackend);
// tests substitute a fake so the leadership state machine is exercised without
// Postgres.
type Backend interface {
	// TryLock attempts to acquire the advisory lock for key on THIS backend.
	// It is called only on standby; on success the backend owns key.
	TryLock(ctx context.Context, key int64) (acquired bool, pid int, err error)
	// HoldsLock reports whether THIS backend's connection currently holds key.
	// It is a read-only pg_locks probe and must NOT acquire the lock:
	// re-acquiring a re-entrant advisory lock masks a lost lock (SDS §3.5).
	HoldsLock(ctx context.Context, key int64) (bool, error)
	// Release frees the advisory lock for key (SDS §3.3). The boolean reports
	// whether the lock was actually held by this session: false means it had
	// already been released server-side (e.g. backend exit), which the caller
	// logs rather than treats as fatal (§3.6).
	Release(ctx context.Context, key int64) (bool, error)
	// Close closes the underlying dedicated connection.
	Close() error
}

// stdBackend is the default Backend, backed by a dedicated *sql.Conn. The
// caller is responsible for ensuring conn is session-stable (see ProbeSessionStable
// and the Scheduler's construction gate).
type stdBackend struct {
	conn *sql.Conn
}

// NewStdBackend wraps conn as a dedicated-lock backend.
func NewStdBackend(conn *sql.Conn) Backend {
	return &stdBackend{conn: conn}
}

// TryLock implements Backend.
func (b *stdBackend) TryLock(ctx context.Context, key int64) (bool, int, error) {
	var acquired bool
	var pid int
	err := b.conn.QueryRowContext(ctx, sqlTryLock, key).Scan(&acquired, &pid)
	if err != nil {
		return false, 0, fmt.Errorf("elector: try advisory lock: %w", err)
	}
	return acquired, pid, nil
}

// HoldsLock implements Backend.
func (b *stdBackend) HoldsLock(ctx context.Context, key int64) (bool, error) {
	var holds bool
	err := b.conn.QueryRowContext(ctx, sqlHolds, key).Scan(&holds)
	if err != nil {
		return false, fmt.Errorf("elector: holds advisory lock: %w", err)
	}
	return holds, nil
}

// Release implements Backend.
func (b *stdBackend) Release(ctx context.Context, key int64) (bool, error) {
	var released bool
	err := b.conn.QueryRowContext(ctx, sqlRelease, key).Scan(&released)
	if err != nil {
		return false, fmt.Errorf("elector: release advisory lock: %w", err)
	}
	return released, nil
}

// Close implements Backend.
func (b *stdBackend) Close() error {
	return b.conn.Close()
}
