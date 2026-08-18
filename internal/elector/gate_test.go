package elector

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestSessionStableFromPIDs(t *testing.T) {
	if !SessionStableFromPIDs(7, 7) {
		t.Fatal("equal pids must be session-stable")
	}
	if SessionStableFromPIDs(7, 8) {
		t.Fatal("different pids must not be session-stable")
	}
}

// fakeRow implements Row by returning a fixed pid (or error) per Scan.
type fakeRow struct {
	pid int
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if p, ok := dest[0].(*int); ok {
		*p = r.pid
	}
	return nil
}

// fakeQuerier returns successive pids (or an error) for each QueryRowContext,
// honoring the caller's context.
type fakeQuerier struct {
	mu   sync.Mutex
	seq  []int
	err  error
	call int
}

func (f *fakeQuerier) QueryRowContext(ctx context.Context, _ string, _ ...any) Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fakeRow{err: err}
	}
	if f.err != nil {
		return fakeRow{err: f.err}
	}
	idx := f.call
	f.call++
	if idx >= len(f.seq) {
		return fakeRow{err: errors.New("no more pid samples")}
	}
	return fakeRow{pid: f.seq[idx]}
}

func TestProbeSessionStableStable(t *testing.T) {
	fq := &fakeQuerier{seq: []int{7, 7}}
	stable, err := ProbeSessionStable(context.Background(), fq)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if !stable {
		t.Fatal("want stable, got unstable")
	}
}

func TestProbeSessionStableInstable(t *testing.T) {
	fq := &fakeQuerier{seq: []int{7, 8}}
	stable, err := ProbeSessionStable(context.Background(), fq)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if stable {
		t.Fatal("want unstable, got stable")
	}
}

func TestProbeSessionStableError(t *testing.T) {
	fq := &fakeQuerier{err: errors.New("connection lost")}
	stable, err := ProbeSessionStable(context.Background(), fq)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if stable {
		t.Fatal("stable must be false on error")
	}
}

func TestProbeSessionStableHonorsContext(t *testing.T) {
	fq := &fakeQuerier{seq: []int{7, 7}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stable, err := ProbeSessionStable(ctx, fq)
	if err == nil {
		t.Fatal("want error on canceled context")
	}
	if stable {
		t.Fatal("stable must be false when the context is canceled")
	}
}

func TestPoolCapacity(t *testing.T) {
	if err := PoolCapacity(0); err != nil {
		t.Fatalf("unlimited (0) should be allowed: %v", err)
	}
	if err := PoolCapacity(5); err != nil {
		t.Fatalf("maxOpen=5 should be allowed: %v", err)
	}
	if err := PoolCapacity(1); !errors.Is(err, ErrSingleConnectionPool) {
		t.Fatalf("maxOpen=1: err = %v; want ErrSingleConnectionPool", err)
	}
}
