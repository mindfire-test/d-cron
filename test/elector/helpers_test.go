package elector_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeBackend struct {
	mu       sync.Mutex
	held     bool
	pid      int
	nextPID  int
	tryErr   error
	holdsErr error
	relErr   error
	released int
	tries    int
	holds    int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{nextPID: 1}
}

func (f *fakeBackend) TryLock(ctx context.Context, _ int64) (bool, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tries++
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	if f.tryErr != nil {
		return false, 0, f.tryErr
	}
	if f.held {
		return false, 0, nil
	}
	f.held = true
	f.pid = f.nextPID
	f.nextPID++
	return true, f.pid, nil
}

func (f *fakeBackend) HoldsLock(ctx context.Context, _ int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.holdsErr != nil {
		return false, f.holdsErr
	}
	return f.held, nil
}

func (f *fakeBackend) Release(ctx context.Context, _ int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.relErr != nil {
		return false, f.relErr
	}
	f.held = false
	return true, nil
}

func (f *fakeBackend) Close() error { return nil }
