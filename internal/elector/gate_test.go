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

// TestPoolCapacityReturnsTypedError asserts that PoolCapacity returns the typed
// *SingleConnectionPoolError (issue #26), not just the sentinel: callers may
// errors.As for the concrete type to read the offending MaxOpen value, and it
// still errors.Is-compares to ErrSingleConnectionPool (tested above).
func TestPoolCapacityReturnsTypedError(t *testing.T) {
	err := PoolCapacity(1)
	var sce *SingleConnectionPoolError
	if !errors.As(err, &sce) {
		t.Fatalf("err = %T; want *SingleConnectionPoolError", err)
	}
	if sce.MaxOpen != 1 {
		t.Errorf("MaxOpen = %d; want 1", sce.MaxOpen)
	}
	// The typed error must still match the sentinel so existing callers are unaffected.
	if !errors.Is(err, ErrSingleConnectionPool) {
		t.Fatal("typed error must also errors.Is against ErrSingleConnectionPool")
	}
}

// keepaliveRow scans two nullable ints (the keepalive GUC probe result).
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

// keepaliveQuerier returns a single row of keepalive GUC values.
type keepaliveQuerier struct {
	i1, i2 *int
	err    error
}

func (q keepaliveQuerier) QueryRowContext(_ context.Context, _ string, _ ...any) Row {
	return keepaliveRow{i1: q.i1, i2: q.i2, err: q.err}
}

func intp(v int) *int { return &v }

func TestKeepaliveUnsafe(t *testing.T) {
	if !KeepaliveUnsafe(0, 0) {
		t.Fatal("both 0 must be unsafe")
	}
	if KeepaliveUnsafe(60, 0) {
		t.Fatal("tcp_keepalives_idle=60 must be safe")
	}
	if KeepaliveUnsafe(0, 1) {
		t.Fatal("client_connection_check_interval=1 must be safe")
	}
}

func TestProbeKeepalive(t *testing.T) {
	got := keepaliveQuerier{i1: intp(60), i2: intp(0)}
	idle, check, err := ProbeKeepalive(context.Background(), got)
	if err != nil {
		t.Fatalf("ProbeKeepalive: %v", err)
	}
	if idle != 60 || check != 0 {
		t.Fatalf("ProbeKeepalive = (%d, %d); want (60, 0)", idle, check)
	}

	// Unset values arrive as NULL and must report as 0.
	got = keepaliveQuerier{}
	idle, check, err = ProbeKeepalive(context.Background(), got)
	if err != nil {
		t.Fatalf("ProbeKeepalive (unset): %v", err)
	}
	if idle != 0 || check != 0 {
		t.Fatalf("unset GUCs = (%d, %d); want (0, 0)", idle, check)
	}
}

func TestProbeKeepaliveError(t *testing.T) {
	want := errors.New("no current_setting on this backend")
	got := keepaliveQuerier{err: want}
	_, _, err := ProbeKeepalive(context.Background(), got)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v; want probe error", err)
	}
}
