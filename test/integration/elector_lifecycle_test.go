//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	dcron "github.com/mindfire-test/d-cron/dcron"
)

// TestGates_RefusesUnsafeStart covers two #28 acceptance points that need no
// leader election: the session-stability assertion and the single-connection
// pool refusal (AC from #13).
func TestGates_RefusesUnsafeStart(t *testing.T) {
	db := mustOpen(t)

	if _, err := dcron.New(db); err == nil {
		t.Fatal("New without WithSessionStableConnection must be refused")
	}

	db2 := mustOpen(t)
	db2.SetMaxOpenConns(1)
	if _, err := dcron.New(db2, dcron.WithSessionStableConnection()); err == nil {
		t.Fatal("New with MaxOpenConns(1) borrowed for the lock must be refused")
	}
}

// TestLeaderFailover_PromotesExactlyOneStandby covers the core AC-02 shape:
// two schedulers race, exactly one becomes leader; killing the leader's
// connection promotes the survivor exactly once. ROLE-AGNOSTIC: either
// replica may win the initial race.
func TestLeaderFailover_PromotesExactlyOneStandby(t *testing.T) {
	ctx := context.Background()
	ns := "it-failover"

	dbs := []*sql.DB{mustOpen(t), mustOpen(t)}
	schedulers := make([]*dcron.Scheduler, 2)
	for i, db := range dbs {
		s, err := dcron.New(
			db,
			dcron.WithNamespace(ns),
			dcron.WithSessionStableConnection(),
			dcron.WithPollInterval(20*time.Millisecond),
			dcron.WithLogger(slog.Default()),
		)
		if err != nil {
			t.Fatalf("replica %d New: %v", i, err)
		}
		if err := s.Add("job", "@every 5ms", func(context.Context) error { return nil }); err != nil {
			t.Fatalf("Add on replica %d: %v", i, err)
		}
		if err := s.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func(s *dcron.Scheduler) {
			dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.Stop(dctx)
		}(s)
		schedulers[i] = s
	}

	// Raw-SQL preflight: prove advisory locks work at the SQL level on a
	// connection that NO scheduler is using. We use a dedicated scratch key
	// (not the real one, which a scheduler already holds on another pooled
	// connection — contending on it would correctly return false and would be
	// indistinguishable from an SQL failure).
	{
		conn, err := mustOpen(t).Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		const scratch = int64(42)
		var ok bool
		var pid int
		if err := conn.QueryRowContext(ctx,
			`SELECT pg_try_advisory_lock($1), pg_backend_pid()`, scratch).
			Scan(&ok, &pid); err != nil {
			t.Fatalf("raw try_lock: %v", err)
		}
		t.Logf("preflight: scratch=%d raw_try_lock=%v pid=%d", scratch, ok, pid)
		if !ok {
			t.Fatal("raw pg_try_advisory_lock failed on a free scratch key — SQL-level problem")
		}
		if _, err := conn.ExecContext(ctx,
			`SELECT pg_advisory_unlock($1)`, scratch); err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}

	// Phase 1: exactly one leader and exactly one standby (either may win).
	waitForStates(t, 15*time.Second, "one leader + one standby", schedulers, func() bool {
		return countLeaders(schedulers) == 1 && countStandbys(schedulers) == 1
	})

	var leader, follower *dcron.Scheduler
	for _, s := range schedulers {
		if s.Leadership() == dcron.LeadershipLeader {
			leader = s
		} else {
			follower = s
		}
	}

	// Kill the leader: its advisory lock dies with its session.
	if err := leader.Stop(context.Background()); err != nil {
		t.Logf("leader Stop returned %v (acceptable during failover)", err)
	}

	// Phase 2: the survivor must be promoted, and it must be the ONLY leader.
	waitForStates(t, 20*time.Second, "survivor promoted after failover", schedulers, func() bool {
		return follower.Leadership() == dcron.LeadershipLeader &&
			countLeaders(schedulers) == 1
	})
}

func countLeaders(ss []*dcron.Scheduler) int {
	n := 0
	for _, s := range ss {
		if s.Leadership() == dcron.LeadershipLeader {
			n++
		}
	}
	return n
}

func countStandbys(ss []*dcron.Scheduler) int {
	n := 0
	for _, s := range ss {
		if s.Leadership() == dcron.LeadershipStandby {
			n++
		}
	}
	return n
}

func statesLabel(scheds []*dcron.Scheduler) string {
	parts := make([]string, len(scheds))
	for i, s := range scheds {
		parts[i] = s.Leadership().String()
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, " "))
}

// waitForStates polls cond and, on timeout, dumps both schedulers' leadership
// states plus every advisory lock row so a hung promotion is diagnosable from
// the failure output alone.
func waitForStates(t *testing.T, budget time.Duration, what string,
	scheds []*dcron.Scheduler, cond func() bool,
) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for !cond() {
		if time.Now().After(deadline) {
			dumpAdvisoryLocks(t)
			t.Fatalf("timed out after %s waiting for %s %s",
				budget, what, statesLabel(scheds))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dumpAdvisoryLocks(t *testing.T) {
	rows, err := testDB.Query(`
SELECT l.pid, l.classid, l.objid, l.granted, a.application_name
FROM pg_locks l LEFT JOIN pg_stat_activity a USING (pid)
WHERE l.locktype = 'advisory'`)
	if err != nil {
		t.Logf("advisory dump: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var pid, classid, objid int64
		var granted bool
		var app interface{}
		if err := rows.Scan(&pid, &classid, &objid, &granted, &app); err == nil {
			t.Logf("advisory: pid=%d classid=%d objid=%d granted=%v app=%v",
				pid, classid, objid, granted, app)
		}
	}
}
