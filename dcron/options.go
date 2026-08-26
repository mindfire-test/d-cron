package dcron

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/mindfire-test/d-cron/metrics"
)

// newInstanceID returns a short random hex id that identifies this scheduler
// process in leadership-transition logs (SDS §3.5).
func newInstanceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// Option configures a Scheduler at construction time.
//
// Options are applied in order on top of the defaults and are passed to New.
// All options have working defaults except the session-stability assertion,
// which defaults to an automatic probe (SDS §3.4).
type Option func(*options)

// options is the resolved scheduler configuration.
type options struct {
	namespace    string
	location     *time.Location
	pollInterval time.Duration
	drainTimeout time.Duration
	logger       *slog.Logger

	instance      string // host-unique id stamped into leadership logs
	sessionStable bool
	lockConn      func(ctx context.Context) (*sql.Conn, error)
	secondsField  bool // parse specs as 6-field cron with a leading seconds field

	// Phase 2 observability configuration.
	hooks []Hook           // issue #39: terminal-outcome notification hooks
	rec   metrics.Recorder // issue #36: metrics sink (Noop when unset)

	// Phase 2 history (issue #34/#35). history=true when WithHistory was called.
	history   bool
	retention time.Duration // <=0 keeps history indefinitely
	schema    string        // default "dcron"
}

// defaultOptions returns the documented defaults.
func defaultOptions() options {
	return options{
		namespace:    "default",
		location:     time.UTC,
		pollInterval: 5 * time.Second,
		drainTimeout: 30 * time.Second,
		logger:       slog.Default(),
		instance:     newInstanceID(),
		rec:          metrics.Noop{},
		schema:       "dcron",
	}
}

// WithNamespace sets the namespace that scopes the scheduler's leadership and
// job identity. The default is "default".
func WithNamespace(namespace string) Option {
	return func(o *options) {
		o.namespace = namespace
	}
}

// WithLocation sets the time zone used to interpret schedules. The default is
// UTC.
func WithLocation(loc *time.Location) Option {
	return func(o *options) {
		o.location = loc
	}
}

// WithPollInterval sets how often a standby retries leadership acquisition
// (with jitter). The default is 5s.
func WithPollInterval(d time.Duration) Option {
	return func(o *options) {
		o.pollInterval = d
	}
}

// WithDrainTimeout bounds how long Stop waits for in-flight jobs before their
// contexts are cancelled. The default is 30s.
func WithDrainTimeout(d time.Duration) Option {
	return func(o *options) {
		o.drainTimeout = d
	}
}

// WithLogger sets the logger used for scheduler diagnostics. The default is
// slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		o.logger = l
	}
}

// WithSessionStableConnection asserts that the connection(s) supplied to New
// are session-stable (a direct Postgres connection or a session-mode pooler).
// d-cron refuses to start unless this or WithDedicatedLockConn/WithDedicatedLockDSN
// is set: a transaction-mode pooler silently corrupts advisory-lock semantics,
// producing two simultaneous leaders and an orphaned lock (SDS §3.4, issue #12).
func WithSessionStableConnection() Option {
	return func(o *options) {
		o.sessionStable = true
	}
}

// WithMetrics supplies the recorder that receives scheduler and job
// observability signals (issue #36, FR-404). The default is a Noop that
// discards everything, so applications that do not use metrics pay nothing.
func WithMetrics(rec metrics.Recorder) Option {
	return func(o *options) {
		if rec != nil {
			o.rec = rec
		}
	}
}

// WithHistory enables durable execution history (SDS §10, issues #34/#35).
// This is OPT-IN: unless called, d-cron creates no tables and no schema, and
// the "zero migrations" Phase-1 guarantee holds. With a positive retention,
// finished executions older than retention are pruned on the leader as an
// internal job. A retention of 0 keeps history indefinitely.
func WithHistory(retention time.Duration) Option {
	return func(o *options) {
		o.history = true
		o.retention = retention
	}
}

// WithSchema sets the database schema used for the opt-in history tables
// (issue #34). The default is "dcron"; the schema is never "public". The value
// must be a lowercase SQL identifier; anything else fails at construction.
func WithSchema(schema string) Option {
	return func(o *options) {
		if schema != "" {
			o.schema = schema
		}
	}
}

// WithDedicatedLockConn supplies a function that opens a dedicated, direct
// connection used exclusively for the advisory lock, bypassing the caller's
// pool and any pooler (the SDS's WithDedicatedLockDSN, expressed
// driver-agnostically). It satisfies the session-stability gate: the operator
// asserts the connection bypasses any pooler, so neither session-stability nor
// pool-capacity checks apply.
func WithDedicatedLockConn(open func(ctx context.Context) (*sql.Conn, error)) Option {
	return func(o *options) {
		o.lockConn = open
	}
}

// WithDedicatedLockDSN tells d-cron to open and own exactly one direct
// connection to dsn, used exclusively for the advisory lock (SDS §3.4, issue
// #12). It satisfies the session-stability gate. A Postgres driver must be
// registered under the name "postgres" (lib/pq or pgx/stdlib); the dedicated
// connection therefore bypasses any pooler the application uses.
func WithDedicatedLockDSN(dsn string) Option {
	return WithDedicatedLockDriver("postgres", dsn)
}

// WithDedicatedLockDriver is WithDedicatedLockDSN for drivers registered
// under a different name — e.g. pgx via `_ "github.com/jackc/pgx/v5/stdlib"`
// which registers "pgx" (issue #24). The application imports the driver, so
// the core stays dependency-free (NFR-401); the dedicated connection still
// bypasses any pooler and satisfies the session-stability gate.
func WithDedicatedLockDriver(driverName, dsn string) Option {
	return func(o *options) {
		o.lockConn = func(ctx context.Context) (*sql.Conn, error) {
			db, err := sql.Open(driverName, dsn)
			if err != nil {
				return nil, err
			}
			return db.Conn(ctx)
		}
	}
}

// WithSecondsField enables 6-field cron schedules (second minute hour
// day-of-month month day-of-week), parsed by clock.ParseSeconds, so a job can
// fire on a sub-minute cadence. It is accepted for compatibility only when
// paired with a driver that supports it; by default schedules are 5-field. See
// issue #15 / FR-212.
func WithSecondsField() Option {
	return func(o *options) {
		o.secondsField = true
	}
}
