package elector

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

// Row is the subset of *sql.Row used by the elector's probes.
type Row interface {
	Scan(dest ...any) error
}

type sqlRow struct{ r *sql.Row }

func (w sqlRow) Scan(dest ...any) error { return w.r.Scan(dest...) }

// Querier is the subset of *sql.Conn used by the session-stability probe.
// Narrowing the interface keeps the probe decoupled from the concrete
// *sql.Conn so it can be faked in unit tests (no live Postgres required).
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) Row
}

type sqlConn struct{ c *sql.Conn }

func (w sqlConn) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	return sqlRow{r: w.c.QueryRowContext(ctx, query, args...)}
}

// NewSQLConn returns a Querier backed by conn. The returned value shares conn;
// closing it is the caller's responsibility.
func NewSQLConn(conn *sql.Conn) Querier {
	return sqlConn{c: conn}
}

// SessionStableFromPIDs reports session stability given two backend-pid
// samples taken on a single connection. Equal samples imply the connection
// maps to one backend for its lifetime (a direct Postgres connection or a
// session-mode pooler); a transaction-mode pooler returns a different pid per
// statement.
func SessionStableFromPIDs(p1, p2 int) bool {
	return p1 == p2
}

// ProbeSessionStable takes two pg_backend_pid() samples on q and reports
// whether they are equal (session-stable). A mismatch, or an error, means q is
// backed by a transaction-mode pooler (or is otherwise unsafe for advisory
// locks) and the caller must refuse to start (SDS §3.4).
func ProbeSessionStable(ctx context.Context, q Querier) (bool, error) {
	var p1, p2 int
	if err := q.QueryRowContext(ctx, sqlPID).Scan(&p1); err != nil {
		return false, fmt.Errorf("elector: probe pid (1st): %w", err)
	}
	if err := q.QueryRowContext(ctx, sqlPID).Scan(&p2); err != nil {
		return false, fmt.Errorf("elector: probe pid (2nd): %w", err)
	}
	return SessionStableFromPIDs(p1, p2), nil
}

// PoolCapacity reports whether maxOpen is safe for advisory-lock election. A
// single-connection pool starves both the lock and the session-stability probe
// (FR-112, SDS §3.4). maxOpen is *sql.DB.Stats().MaxOpenConnections; 0 means
// unlimited and is permitted.
func PoolCapacity(maxOpen int) error {
	if maxOpen == 1 {
		return &SingleConnectionPoolError{MaxOpen: maxOpen}
	}
	return nil
}

const sqlKeepalive = `SELECT NULLIF(current_setting('tcp_keepalives_idle', true), '')::int, ` +
	`NULLIF(current_setting('client_connection_check_interval', true), '')::int`

// KeepaliveUnsafe reports whether the given settings leave the lock vulnerable
// to an unbounded hold: with both tcp_keepalives_idle and
// client_connection_check_interval at 0, a dead or partitioned leader blocks in
// recv() and the lock is held until the OS-level keepalive expires — hours at
// Linux defaults.
func KeepaliveUnsafe(idle, connCheck int) bool {
	return idle == 0 && connCheck == 0
}

// ProbeKeepalive reads tcp_keepalives_idle and
// client_connection_check_interval through q. Unset values report as 0 (their
// effective default). It is best-effort: the elector logs a WARN but never
// fails startup on a probe error.
func ProbeKeepalive(ctx context.Context, q Querier) (idle, connCheck int, err error) {
	var i1, i2 *int
	if err := q.QueryRowContext(ctx, sqlKeepalive).Scan(&i1, &i2); err != nil {
		return 0, 0, fmt.Errorf("elector: keepalive probe: %w", err)
	}
	if i1 != nil {
		idle = *i1
	}
	if i2 != nil {
		connCheck = *i2
	}
	return idle, connCheck, nil
}

// WarnKeepalive probes the keepalive GUCs and, when both are 0, logs a loud
// WARN that a dead or partitioned leader will hold the lock for hours with no
// replica promoted (SDS §12 row 3). It returns true when the configuration is
// unsafe. A probe error is logged at Debug and treated as unknown, never fatal.
func WarnKeepalive(ctx context.Context, q Querier, log *slog.Logger) bool {
	if log == nil {
		log = slog.Default()
	}
	idle, connCheck, err := ProbeKeepalive(ctx, q)
	if err != nil {
		log.Debug("elector: keepalive probe unavailable", "err", err)
		return false
	}
	if KeepaliveUnsafe(idle, connCheck) {
		log.Warn("elector: TCP keepalives are disabled on the lock connection; a dead or partitioned leader will hold the lock for hours and NO replica will be promoted (SDS §12 row 3). Set tcp_keepalives_idle (recommended 60-120s) and client_connection_check_interval, or supply a dedicated connection that sets them.",
			"tcp_keepalives_idle", idle, "client_connection_check_interval", connCheck)
		return true
	}
	log.Info("elector: TCP keepalives enabled",
		"tcp_keepalives_idle", idle, "client_connection_check_interval", connCheck)
	return false
}
