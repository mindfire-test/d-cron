package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/mindfire-test/d-cron/internal/elector"
)

// schemaID matches a SQL identifier suitable for interpolation into a
// qualified name. Only the schema name is interpolated (after validation); the
// table and index names are fixed constants, and every data value is passed as
// a bind parameter (NFR-503, issue #34).
var schemaID = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ErrInvalidSchema is returned when the configured schema name fails the
// allowlist (issue #34): non-lowercase identifiers, quoted identifiers, and
// anything else that would allow an injection are refused rather than
// interpolated.
var ErrInvalidSchema = errors.New("store: schema name must be a lowercase SQL identifier")

// validateSchema enforces the allowlist plus the never-public rule (issue #34,
// NFR-503): only then may the identifier be interpolated into DDL.
func validateSchema(schema string) error {
	if schema == "public" {
		return fmt.Errorf("%w: %q is reserved; d-cron never creates its tables in public", ErrInvalidSchema, schema)
	}
	if !schemaID.MatchString(schema) {
		return fmt.Errorf("%w: %q", ErrInvalidSchema, schema)
	}
	return nil
}

// schemaLockKey derives the advisory-lock key that serialises schema
// migrations for one schema name. It is distinct from the leadership key for
// the same application namespace because it hashes a different input
// ("d-cron:migration:<schema>" vs "d-cron:v1:<namespace>"), so acquiring a
// leadership lock never blocks a migration and vice versa (SDS §10, issue
// #34/FR-504).
func schemaLockKey(schema string) int64 {
	return elector.LockKey("d-cron:migration:" + schema)
}

// migrationDDL is the idempotent DDL applied under the migration lock. schema
// has already passed the allowlist.
func migrationDDL(schema string) []string {
	return []string{
		`CREATE SCHEMA IF NOT EXISTS ` + schema,
		`CREATE TABLE IF NOT EXISTS ` + schema + `.execution (
			id            bigserial PRIMARY KEY,
			namespace     text        NOT NULL,
			job_name      text        NOT NULL,
			scheduled_at  timestamptz NOT NULL,
			started_at    timestamptz NOT NULL,
			finished_at   timestamptz,
			status        text        NOT NULL,
			attempt       int         NOT NULL DEFAULT 1,
			duration_ms   bigint,
			error         text,
			instance_id   text        NOT NULL,
			leader_epoch  bigint      NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS ` + schema + `.execution_job_time_idx
			ON ` + schema + `.execution (namespace, job_name, scheduled_at DESC)`,
	}
}

// Migrate creates the schema and table if they do not exist, guarded by a
// separate advisory lock so N replicas starting at once do not race (SDS §10).
// It is safe to call from every replica at startup: the DDL is idempotent and
// the lock serialises the CREATEs. The lock is released before returning, so
// the reserved connection is returned to the pool cleanly.
//
// The migration borrows a dedicated conn from db: the advisory lock is a
// session lock, so it must be acquired, held, and released on one underlying
// backend without the pool recycling the connection mid-flight.
func Migrate(ctx context.Context, db *sql.DB, schema string) error {
	if err := validateSchema(schema); err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: migrate: borrow conn: %w", err)
	}
	defer conn.Close()

	key := schemaLockKey(schema)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("store: migrate: acquire lock: %w", err)
	}
	defer func() {
		// Best-effort release; losing the lock releases on backend exit anyway.
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migrate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range migrationDDL(schema) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: migrate: apply: %w", err)
		}
	}
	return tx.Commit()
}
