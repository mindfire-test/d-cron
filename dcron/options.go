package dcron

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/mindfire-test/d-cron/metrics"
	"github.com/mindfire-test/d-cron/otel"
)

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
// which requires an explicit assertion via WithSessionStableConnection or a dedicated lock connection.
type Option func(*options)

type options struct {
	namespace    string
	location     *time.Location
	pollInterval time.Duration
	drainTimeout time.Duration
	logger       *slog.Logger

	instance      string
	sessionStable bool
	lockConn      func(ctx context.Context) (*sql.Conn, error)
	lockCloser    func() error
	secondsField  bool

	hooks []Hook
	rec   metrics.Recorder

	history   bool
	retention time.Duration
	schema    string

	startupTimeout   time.Duration
	pollTimeout      time.Duration
	dbTimeout        time.Duration
	tracer           otel.Tracer
	leaderForwardURL string
	handlers         map[string]JobFunc
}

func defaultOptions() options {
	return options{
		namespace:      "default",
		location:       time.UTC,
		pollInterval:   5 * time.Second,
		drainTimeout:   30 * time.Second,
		startupTimeout: 15 * time.Second,
		pollTimeout:    5 * time.Second,
		dbTimeout:      5 * time.Second,
		logger:         slog.Default(),
		instance:       newInstanceID(),
		rec:            metrics.Noop{},
		schema:         "dcron",
		tracer:         otel.NoopTracer{},
		handlers:       make(map[string]JobFunc),
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

// WithPollInterval sets how often a standby polls for leadership acquisition
// and how often the active leader verifies lock liveness via HoldsLock (with ±20% jitter).
// The default is 5s.
//
// Performance & Database Load Considerations:
// On every polling interval:
//   - Each standby replica executes SELECT pg_try_advisory_lock($1), pg_backend_pid().
//   - The active leader executes a read-only pg_locks query (HoldsLock) to verify session ownership.
//
// While pg_locks is an in-memory lock table scan in PostgreSQL, setting aggressive sub-second
// intervals (e.g. < 1s) materially increases database query volume and connection load, especially
// in deployments with many standby replicas.
//
// For production deployments, an interval between 3s and 5s is recommended. Fast failover
// should be achieved by configuring aggressive TCP keepalive timeouts rather than tight database polling loops.
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
// slog.Default(). Passing nil resets the logger to slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l == nil {
			o.logger = slog.Default()
			return
		}
		o.logger = l
	}
}

// WithSessionStableConnection asserts that the connection(s) supplied to New
// are session-stable (a direct Postgres connection or a session-mode pooler).
func WithSessionStableConnection() Option {
	return func(o *options) {
		o.sessionStable = true
	}
}

// WithMetrics supplies the recorder that receives scheduler and job observability signals.
func WithMetrics(rec metrics.Recorder) Option {
	return func(o *options) {
		if rec != nil {
			o.rec = rec
		}
	}
}

// WithHistory enables durable execution history.
func WithHistory(retention time.Duration) Option {
	return func(o *options) {
		o.history = true
		o.retention = retention
	}
}

// WithSchema sets the database schema used for history tables.
func WithSchema(schema string) Option {
	return func(o *options) {
		if schema != "" {
			o.schema = schema
		}
	}
}

// WithDedicatedLockConn supplies a function that opens a dedicated connection for the lock.
func WithDedicatedLockConn(open func(ctx context.Context) (*sql.Conn, error)) Option {
	return func(o *options) {
		o.lockConn = open
	}
}

// WithDedicatedLockDSN configures d-cron to open a dedicated connection to dsn for the lock.
func WithDedicatedLockDSN(dsn string) Option {
	return WithDedicatedLockDriver("postgres", dsn)
}

// WithDedicatedLockDriver is WithDedicatedLockDSN for custom registered driver names.
func WithDedicatedLockDriver(driverName, dsn string) Option {
	return func(o *options) {
		var openedDB *sql.DB
		o.lockConn = func(ctx context.Context) (*sql.Conn, error) {
			db, err := sql.Open(driverName, dsn)
			if err != nil {
				return nil, err
			}
			openedDB = db
			return db.Conn(ctx)
		}
		o.lockCloser = func() error {
			if openedDB != nil {
				return openedDB.Close()
			}
			return nil
		}
	}
}

// WithSecondsField enables 6-field cron schedules.
func WithSecondsField() Option {
	return func(o *options) {
		o.secondsField = true
	}
}

// WithStartupTimeout sets the maximum duration allowed for startup database operations
// (obtaining the dedicated lock connection, probing keepalives, and running migrations).
// The default is 15 seconds.
func WithStartupTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.startupTimeout = d
		}
	}
}

// WithPollTimeout sets the per-attempt timeout for leadership probe queries.
// The default is 5 seconds.
func WithPollTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.pollTimeout = d
		}
	}
}

// WithDBTimeout sets the per-operation timeout for history store and admin mutation database queries.
// The default is 5 seconds.
func WithDBTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.dbTimeout = d
		}
	}
}

// WithOpTimeout is an alias for WithDBTimeout, setting the per-operation timeout for internal database queries.
func WithOpTimeout(d time.Duration) Option {
	return WithDBTimeout(d)
}

// WithLeaderForwardURL sets the URL to which a standby replica will forward administrative mutation requests.
func WithLeaderForwardURL(url string) Option {
	return func(o *options) {
		o.leaderForwardURL = url
	}
}

// WithJobHandler registers a JobFunc by job name so a standby promoted to leader
// can restore dynamic jobs registered in the database across failover.
func WithJobHandler(name string, fn JobFunc) Option {
	return func(o *options) {
		if o.handlers == nil {
			o.handlers = make(map[string]JobFunc)
		}
		if fn != nil {
			o.handlers[name] = fn
		}
	}
}

// WithTracer attaches an OpenTelemetry tracer for instrumenting job executions.
func WithTracer(t otel.Tracer) Option {
	return func(o *options) {
		if t != nil {
			o.tracer = t
		}
	}
}
