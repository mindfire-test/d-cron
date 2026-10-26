//go:build integration

package integration

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/internal/elector"
)

// TestAdvisoryHoldsLockRoundTrip pins the pg_locks ownership probe used by the
// elector's leader self-check. Regression check for advisory lock storage:
// the single-bigint advisory lock is stored split as two signed int4 halves
// (classid = key>>32, objid = key&0xFFFFFFFF), so the low half can be NEGATIVE.
// An exact bigint comparison then never matches for keys whose low word has bit 31
// set — HoldsLock falsely reported "not held" on the session that actually owned the
// lock, causing unwanted demotions. Each key below must report held by its own
// holder and not-held after release.
func TestAdvisoryHoldsLockRoundTrip(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	be := elector.NewStdBackend(conn)

	keys := []int64{
		elector.LockKey("it-failover"),
		elector.LockKey("dcron_it_migrate"),
		-1,
		0x7fffffffffffffff,
		2147483649,
		-6078668925632109360,
		918273645,
	}

	for _, key := range keys {
		key := key
		t.Run(electorKeyLabel(key), func(t *testing.T) {
			var acquired bool
			if err := conn.QueryRowContext(ctx,
				`SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
				t.Fatalf("try_lock: %v", err)
			}
			if !acquired {
				t.Fatal("try_lock reported not acquired")
			}

			holds, err := be.HoldsLock(ctx, key)
			if err != nil {
				t.Fatalf("HoldsLock: %v", err)
			}
			if !holds {
				t.Fatal("HoldsLock=false on the session that OWNS the lock — leadership flap regression")
			}

			other, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			otherBe := elector.NewStdBackend(other)
			otherHolds, err := otherBe.HoldsLock(ctx, key)
			_ = other.Close()
			if err != nil {
				t.Fatalf("other HoldsLock: %v", err)
			}
			if otherHolds {
				t.Fatal("HoldsLock=true on a session that does NOT own the lock")
			}

			released, err := be.Release(ctx, key)
			if err != nil {
				t.Fatalf("Release: %v", err)
			}
			if !released {
				t.Fatal("Release reported not held")
			}
			holdsAfter, err := be.HoldsLock(ctx, key)
			if err != nil {
				t.Fatalf("HoldsLock after release: %v", err)
			}
			if holdsAfter {
				t.Fatal("HoldsLock=true after release")
			}
		})
	}
}

func electorKeyLabel(k int64) string {
	if k < 0 {
		return "neg_0x" + uint64String(uint64(k))
	}
	return "pos_0x" + uint64String(uint64(k))
}

func uint64String(u uint64) string {
	const hex = "0123456789abcdef"
	var b [16]byte
	for i := len(b) - 1; i >= 0; i-- {
		b[i] = hex[u&0xf]
		u >>= 4
	}
	return string(b[:])
}

func TestHealthCheck_DedicatedLockConnection(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()

	// 1. Healthy pool and healthy lock connection
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sched, err := dcron.New(db,
		dcron.WithNamespace("it-healthcheck"),
		dcron.WithDedicatedLockConn(func(ctx context.Context) (*sql.Conn, error) {
			return conn, nil
		}),
	)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("New: %v", err)
	}

	if err := sched.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck expected healthy, got: %v", err)
	}

	// 2. Close ONLY the dedicated lock connection, while db pool remains open and healthy
	_ = conn.Close()

	// db pool ping still succeeds because db connection pool has fresh connections
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db pool ping failed: %v", err)
	}

	// HealthCheck MUST fail because dedicated lock connection is broken!
	err = sched.HealthCheck(ctx)
	if err == nil {
		t.Fatal("HealthCheck expected failure when dedicated lock connection is closed, but got nil (pool masked broken connection!)")
	}
	if !strings.Contains(err.Error(), "dedicated lock connection") {
		t.Fatalf("HealthCheck error = %v; want mention of dedicated lock connection", err)
	}
}

func BenchmarkHoldsLock(b *testing.B) {
	if testDB == nil {
		b.Skip("PostgreSQL integration pool not initialized")
	}
	ctx := context.Background()
	conn, err := testDB.Conn(ctx)
	if err != nil {
		b.Fatalf("Conn: %v", err)
	}
	defer conn.Close()

	be := elector.NewStdBackend(conn)
	key := int64(9876543210)
	acquired, _, err := be.TryLock(ctx, key)
	if err != nil || !acquired {
		b.Fatalf("TryLock: acquired=%v, err=%v", acquired, err)
	}
	defer func() { _, _ = be.Release(ctx, key) }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		holds, err := be.HoldsLock(ctx, key)
		if err != nil || !holds {
			b.Fatalf("HoldsLock: holds=%v, err=%v", holds, err)
		}
	}
}
