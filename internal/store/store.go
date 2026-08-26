package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Status is the terminal state of one execution row (SDS §10, issue #35).
type Status string

const (
	StatusRunning  Status = "running"
	StatusSuccess  Status = "success"
	StatusFailed   Status = "failed"
	StatusPanicked Status = "panicked"
	StatusSkipped  Status = "skipped"
	StatusTimeout  Status = "timeout"
)

// Execution is the value written to the dcron.execution table (SDS §10).
type Execution struct {
	Namespace   string
	JobName     string
	ScheduledAt time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	Status      Status
	Attempt     int
	DurationMs  int64
	Error       string
	InstanceID  string
	LeaderEpoch int64
}

// Store persists and prunes execution history in the configured schema. It is
// created by New and never used unless history is enabled (opt-in, SDS §10).
type Store struct {
	db     *sql.DB
	schema string

	exec string
}

// New returns a Store over db using schema (default "dcron"). It validates the
// schema allowlist but does not run the migration; call Migrate explicitly (the
// scheduler does this at construction when history is enabled).
func New(db *sql.DB, schema string) (*Store, error) {
	if schema == "" {
		schema = "dcron"
	}
	if err := validateSchema(schema); err != nil {
		return nil, err
	}
	return &Store{
		db:     db,
		schema: schema,
		exec:   schema + ".execution",
	}, nil
}

// Schema returns the configured schema name.
func (s *Store) Schema() string { return s.schema }

// Record inserts the opening "status = running" row and returns the new id.
// The write is stamped with the leader epoch (Phase 3 adds the guarded form;
// Phase 2 stamps only, per SDS §10).
func (s *Store) Record(ctx context.Context, e Execution) (int64, error) {
	q := `INSERT INTO ` + s.exec + `
		(namespace, job_name, scheduled_at, started_at, status, attempt, instance_id, leader_epoch)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`
	var id int64
	err := s.db.QueryRowContext(
		ctx, q,
		e.Namespace, e.JobName, e.ScheduledAt, e.StartedAt,
		string(e.Status), e.Attempt, e.InstanceID, e.LeaderEpoch,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: record execution: %w", err)
	}
	return id, nil
}

// Finish updates a running row to its terminal state. It reports how many rows
// were affected so the caller can detect a fenced write (zero rows ⇒ stale
// epoch, Phase 3). History write failures are surfaced to the caller, which
// must never fail the job itself (SDS §10 / issue #35).
func (s *Store) Finish(ctx context.Context, id int64, e Execution) (int64, error) {
	q := `UPDATE ` + s.exec + `
		SET status = $1, finished_at = $2, duration_ms = $3, error = NULLIF($4, ''), attempt = $5
		WHERE id = $6`
	res, err := s.db.ExecContext(
		ctx, q,
		string(e.Status), e.FinishedAt, e.DurationMs, e.Error, e.Attempt, id,
	)
	if err != nil {
		return 0, fmt.Errorf("store: finish execution: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: finish execution rows: %w", err)
	}
	return rows, nil
}

// Recent returns the most recent executions for a namespace and job (empty job
// matches all), ordered by scheduled_at descending, limited to limit rows. It
// backs the dashboard history panel (issue #35/FR-406).
func (s *Store) Recent(ctx context.Context, namespace, job string, limit int) ([]Execution, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, namespace, job_name, scheduled_at, started_at, finished_at, status,
	        attempt, duration_ms, error, instance_id, leader_epoch
	      FROM ` + s.exec + `
	      WHERE namespace = $1 AND ($2 = '' OR job_name = $2)
	      ORDER BY scheduled_at DESC
	      LIMIT $3`
	rows, err := s.db.QueryContext(ctx, q, namespace, job, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent executions: %w", err)
	}
	defer rows.Close()
	var out []Execution
	for rows.Next() {
		var e Execution
		var finished sql.NullTime
		var dur sql.NullInt64
		var errText sql.NullString
		if err := rows.Scan(new(int64), &e.Namespace, &e.JobName, &e.ScheduledAt, &e.StartedAt,
			&finished, &e.Status, &e.Attempt, &dur, &errText, &e.InstanceID, &e.LeaderEpoch); err != nil {
			return nil, fmt.Errorf("store: scan execution: %w", err)
		}
		e.FinishedAt = finished.Time
		if dur.Valid {
			e.DurationMs = dur.Int64
		}
		if errText.Valid {
			e.Error = errText.String
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: recent executions rows: %w", err)
	}
	return out, nil
}

// Prune deletes finished executions older than retention, returning how many
// rows were removed. It runs on the leader as an internal job (SDS §10, issue
// #35). A zero-length retention disables pruning (returns 0, nil).
func (s *Store) Prune(ctx context.Context, namespace string, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	q := `DELETE FROM ` + s.exec + `
		WHERE namespace = $1 AND status <> $2
		  AND finished_at < now() - ($3 * interval '1 millisecond')`
	res, err := s.db.ExecContext(ctx, q, namespace, string(StatusRunning), retention.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("store: prune executions: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune rows: %w", err)
	}
	return rows, nil
}
