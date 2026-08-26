package dcron

import (
	"context"

	"github.com/mindfire-test/d-cron/internal/elector"
)

// LockBackend is the pluggable leadership-lock contract behind NewWithBackend.
// It mirrors the operations d-cron needs from its advisory-lock store, so
// embedders and tests can supply an alternative backend (or a deterministic
// fake) without touching the database. TryLock/HoldsLock/Release receive the
// resolved namespace lock key (see Scheduler.Key).
type LockBackend interface {
	// TryLock attempts to acquire the lock; it reports whether the caller
	// became the holder and the current holder's pid.
	TryLock(ctx context.Context, key int64) (acquired bool, pid int, err error)
	// HoldsLock reports whether this session still owns the lock.
	HoldsLock(ctx context.Context, key int64) (bool, error)
	// Release surrenders the lock; it reports whether the caller held it.
	Release(ctx context.Context, key int64) (bool, error)
	// Close releases backend resources. It must be safe to call once.
	Close() error
}

// NewWithBackend builds a Scheduler around a caller-supplied LockBackend,
// bypassing the database entirely: no connection pool gates apply and no SQL
// is executed. It exists for embedding d-cron's scheduling core on
// alternative coordination stores and for deterministic tests.
func NewWithBackend(backend LockBackend, opts ...Option) *Scheduler {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return newWithBackend(lockBackendAdapter{b: backend}, nil, cfg, nil)
}

type lockBackendAdapter struct{ b LockBackend }

func (a lockBackendAdapter) TryLock(ctx context.Context, key int64) (bool, int, error) {
	return a.b.TryLock(ctx, key)
}

func (a lockBackendAdapter) HoldsLock(ctx context.Context, key int64) (bool, error) {
	return a.b.HoldsLock(ctx, key)
}

func (a lockBackendAdapter) Release(ctx context.Context, key int64) (bool, error) {
	return a.b.Release(ctx, key)
}

func (a lockBackendAdapter) Close() error { return a.b.Close() }

var _ elector.Backend = lockBackendAdapter{}
