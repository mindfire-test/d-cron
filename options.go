package dcron

import (
	"log/slog"
	"time"
)

// Option configures a Scheduler at construction time.
//
// Options are applied in order on top of the defaults and are passed to New.
// All options have working defaults; the mandatory session-stability
// assertion is added in Phase 1 (SDS §3.4).
type Option func(*options)

// options is the resolved scheduler configuration.
type options struct {
	namespace    string
	location     *time.Location
	pollInterval time.Duration
	drainTimeout time.Duration
	logger       *slog.Logger
}

// defaultOptions returns the documented defaults.
func defaultOptions() options {
	return options{
		namespace:    "default",
		location:     time.UTC,
		pollInterval: 5 * time.Second,
		drainTimeout: 30 * time.Second,
		logger:       slog.Default(),
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
