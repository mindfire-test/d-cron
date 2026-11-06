//go:build integration

package integration

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfiredigital/d-cron/dcron"
	"github.com/mindfiredigital/d-cron/internal/elector"
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

// TestE2E_DrainBeforeRelease_PreventsCrossReplicaOverlap verifies that when WithDrainBeforeRelease()
// is configured on the primary leader, shutting it down mid-job drains in-flight jobs before
// releasing the advisory lock, preventing the newly promoted standby from executing concurrently.
func TestE2E_DrainBeforeRelease_PreventsCrossReplicaOverlap(t *testing.T) {
	if testDB == nil {
		t.Skip("PostgreSQL integration pool not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := mustOpen(t)
	const namespace = "it-drain-before-release"

	var activeRuns atomic.Int32
	var maxConcurrent atomic.Int32
	jobStarted := make(chan struct{})
	unblockJob := make(chan struct{})

	// 1. Create Leader replica with WithDrainBeforeRelease
	s1, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
		dcron.WithDrainBeforeRelease(),
		dcron.WithDrainTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}

	workFn := func(replica string) dcron.JobFunc {
		return func(ctx context.Context) error {
			cur := activeRuns.Add(1)
			defer activeRuns.Add(-1)

			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}

			if replica == "leader" {
				select {
				case <-jobStarted:
				default:
					close(jobStarted)
				}
				<-unblockJob
			}
			return nil
		}
	}

	err = s1.Add("overlap-check-job", "@every 50ms", workFn("leader"), dcron.WithNoOverlap())
	if err != nil {
		t.Fatalf("s1.Add: %v", err)
	}

	if err := s1.Start(ctx); err != nil {
		t.Fatalf("Start s1: %v", err)
	}

	// 2. Create Standby replica
	s2, err := dcron.New(
		db,
		dcron.WithNamespace(namespace),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}

	err = s2.Add("overlap-check-job", "@every 50ms", workFn("standby"), dcron.WithNoOverlap())
	if err != nil {
		t.Fatalf("s2.Add: %v", err)
	}

	if err := s2.Start(ctx); err != nil {
		t.Fatalf("Start s2: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = s2.Stop(stopCtx)
	}()

	// Wait until s1's job starts executing
	select {
	case <-jobStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for leader job to start")
	}

	// 3. Stop s1 while the job is still running
	s1Stopped := make(chan struct{})
	go func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer stopCancel()
		_ = s1.Stop(stopCtx)
		close(s1Stopped)
	}()

	// Wait 100ms to allow standby time to attempt acquisition if lock were prematurely released
	time.Sleep(100 * time.Millisecond)

	// Now unblock the leader's running job so drain finishes
	close(unblockJob)
	<-s1Stopped

	// Verify that at no point did s1 and s2 execute the job concurrently
	if max := maxConcurrent.Load(); max > 1 {
		t.Fatalf("detected %d concurrent executions across replicas; WithDrainBeforeRelease must ensure max concurrent == 1", max)
	}
}
