//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/mindfire-test/d-cron/internal/elector"
)

// TestAdvisoryHoldsLockRoundTrip pins the pg_locks ownership probe used by the
// elector's leader self-check (issue #6, SDS §3.5). Regression for a real
// bug found running the #28 suite: the single-bigint advisory lock is stored
// split as two signed int4 halves (classid = key>>32, objid = key&0xFFFFFFFF),
// so the low half can be NEGATIVE. An exact bigint comparison then never
// matches for keys whose low word has bit 31 set — HoldsLock falsely reported
// "not held" on the session that actually owned the lock, the leader demoted
// on every poll, and failover deadlocked. Each key below must report held by
// its own holder and not-held after release.
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
