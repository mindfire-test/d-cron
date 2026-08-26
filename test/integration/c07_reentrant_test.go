//go:build integration

package integration

import (
	"context"
	"testing"
)

// TestC07_ReentrantAdvisoryLock is the C-07 regression from issue #28:
// PostgreSQL advisory locks are re-entrant per session. A double try_lock on
// ONE connection returns true twice, and a SINGLE unlock leaves it held.
// d-cron's elector must never rely on unlock-count semantics.
func TestC07_ReentrantAdvisoryLock(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	const key = int64(918273645)

	tryLock := func() bool {
		var acquired bool
		if err := conn.QueryRowContext(ctx,
			`SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
			t.Fatalf("try_lock: %v", err)
		}
		return acquired
	}
	unlockOnce := func() {
		var released bool
		if err := conn.QueryRowContext(ctx,
			`SELECT pg_advisory_unlock($1)`, key).Scan(&released); err != nil {
			t.Fatalf("unlock: %v", err)
		}
		if !released {
			t.Fatal("first unlock reported nothing held")
		}
	}

	if !tryLock() || !tryLock() {
		t.Fatal("both nested try_locks must succeed on the same session (re-entrancy)")
	}
	unlockOnce()

	var stillHeld bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock($1)`, key).Scan(&stillHeld); err != nil {
		t.Fatal(err)
	}
	if !stillHeld {
		t.Fatal("lock must STILL be held after one of two acquires (C-07)")
	}

	if !tryLock() {
		t.Fatal("re-acquire should succeed")
	}
	for i := 0; i < 3; i++ {
		var ok bool
		_ = conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, key).Scan(&ok)
	}
}
