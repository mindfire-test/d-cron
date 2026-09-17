package dcron

import (
	"context"

	"github.com/mindfire-test/d-cron/internal/elector"
)

/* LockBackend is the pluggable leadership-lock contract behind NewWithBackend. */
type LockBackend interface {
	/* TryLock attempts to acquire the lock; it reports whether the caller became the holder and the current holder's pid. */
	TryLock(ctx context.Context, key int64) (acquired bool, pid int, err error)
	/* HoldsLock reports whether this session still owns the lock. */
	HoldsLock(ctx context.Context, key int64) (bool, error)
	/* Release surrenders the lock; it reports whether the caller held it. */
	Release(ctx context.Context, key int64) (bool, error)
	/* Close releases backend resources. It must be safe to call once. */
	Close() error
}

/* NewWithBackend builds a Scheduler around a caller-supplied LockBackend. */
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
