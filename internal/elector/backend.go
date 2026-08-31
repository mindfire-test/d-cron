package elector

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	sqlPID     = `SELECT pg_backend_pid()`
	sqlTryLock = `SELECT pg_try_advisory_lock($1), pg_backend_pid()`

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

type stdBackend struct {
	conn *sql.Conn
}

// NewStdBackend wraps conn as a dedicated-lock backend (issue #7, FR-104/FR-508).
// The reserved *sql.Conn is held for the entire leadership term and reduces
// pool capacity by 1.
func NewStdBackend(conn *sql.Conn) Backend {
	return &stdBackend{conn: conn}
}

// TryLock implements Backend using non-blocking pg_try_advisory_lock (issue #7, FR-102).
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
