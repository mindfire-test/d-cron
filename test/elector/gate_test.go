package elector_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mindfire-test/d-cron/internal/elector"
)

func TestSessionStableFromPIDs(t *testing.T) {
	if !elector.SessionStableFromPIDs(7, 7) {
		t.Fatal("equal pids must be session-stable")
	}
	if elector.SessionStableFromPIDs(7, 8) {
		t.Fatal("different pids must not be session-stable")
	}
}

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

type fakeQuerier struct {
	mu   sync.Mutex
	seq  []int
	err  error
	call int
}

func (f *fakeQuerier) QueryRowContext(ctx context.Context, _ string, _ ...any) elector.Row {
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
	stable, err := elector.ProbeSessionStable(context.Background(), fq)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if !stable {
		t.Fatal("want stable, got unstable")
	}
}

func TestProbeSessionStableInstable(t *testing.T) {
	fq := &fakeQuerier{seq: []int{7, 8}}
	stable, err := elector.ProbeSessionStable(context.Background(), fq)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if stable {
		t.Fatal("want unstable, got stable")
	}
}

func TestProbeSessionStableError(t *testing.T) {
	fq := &fakeQuerier{err: errors.New("connection lost")}
	stable, err := elector.ProbeSessionStable(context.Background(), fq)
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
	stable, err := elector.ProbeSessionStable(ctx, fq)
	if err == nil {
		t.Fatal("want error on canceled context")
	}
	if stable {
		t.Fatal("stable must be false when the context is canceled")
	}
}

func TestPoolCapacity(t *testing.T) {
	if err := elector.PoolCapacity(0); err != nil {
		t.Fatalf("unlimited (0) should be allowed: %v", err)
	}
	if err := elector.PoolCapacity(5); err != nil {
		t.Fatalf("maxOpen=5 should be allowed: %v", err)
	}
	if err := elector.PoolCapacity(1); !errors.Is(err, elector.ErrSingleConnectionPool) {
		t.Fatalf("maxOpen=1: err = %v; want elector.ErrSingleConnectionPool", err)
	}
}

// TestPoolCapacityReturnsTypedError asserts that elector.PoolCapacity returns the typed
// *SingleConnectionPoolError (issue #26), not just the sentinel: callers may
// errors.As for the concrete type to read the offending MaxOpen value, and it
// still errors.Is-compares to elector.ErrSingleConnectionPool (tested above).
func TestPoolCapacityReturnsTypedError(t *testing.T) {
	err := elector.PoolCapacity(1)
	var sce *elector.SingleConnectionPoolError
	if !errors.As(err, &sce) {
		t.Fatalf("err = %T; want *SingleConnectionPoolError", err)
	}
	if sce.MaxOpen != 1 {
		t.Errorf("MaxOpen = %d; want 1", sce.MaxOpen)
	}

	if !errors.Is(err, elector.ErrSingleConnectionPool) {
		t.Fatal("typed error must also errors.Is against elector.ErrSingleConnectionPool")
	}
}

type keepaliveRow struct {
	i1, i2 *int
	err    error
}

func (r keepaliveRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) >= 2 {
		if p1, ok := dest[0].(**int); ok {
			*p1 = r.i1
		}
		if p2, ok := dest[1].(**int); ok {
			*p2 = r.i2
		}
	}
	return nil
}

type keepaliveQuerier struct {
	i1, i2 *int
	err    error
}

func (q keepaliveQuerier) QueryRowContext(_ context.Context, _ string, _ ...any) elector.Row {
	return keepaliveRow{i1: q.i1, i2: q.i2, err: q.err}
}

func intp(v int) *int { return &v }

func TestKeepaliveUnsafe(t *testing.T) {
	if !elector.KeepaliveUnsafe(0, 0) {
		t.Fatal("both 0 must be unsafe")
	}
	if elector.KeepaliveUnsafe(60, 0) {
		t.Fatal("tcp_keepalives_idle=60 must be safe")
	}
	if elector.KeepaliveUnsafe(0, 1) {
		t.Fatal("client_connection_check_interval=1 must be safe")
	}
}

func TestProbeKeepalive(t *testing.T) {
	got := keepaliveQuerier{i1: intp(60), i2: intp(0)}
	idle, check, err := elector.ProbeKeepalive(context.Background(), got)
	if err != nil {
		t.Fatalf("elector.ProbeKeepalive: %v", err)
	}
	if idle != 60 || check != 0 {
		t.Fatalf("elector.ProbeKeepalive = (%d, %d); want (60, 0)", idle, check)
	}

	got = keepaliveQuerier{}
	idle, check, err = elector.ProbeKeepalive(context.Background(), got)
	if err != nil {
		t.Fatalf("elector.ProbeKeepalive (unset): %v", err)
	}
	if idle != 0 || check != 0 {
		t.Fatalf("unset GUCs = (%d, %d); want (0, 0)", idle, check)
	}
}

func TestProbeKeepaliveError(t *testing.T) {
	want := errors.New("no current_setting on this backend")
	got := keepaliveQuerier{err: want}
	_, _, err := elector.ProbeKeepalive(context.Background(), got)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v; want probe error", err)
	}
}
