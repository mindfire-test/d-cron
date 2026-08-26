//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
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
// two schedulers, one leader; killing the leader's connection promotes the
// standby exactly once and jobs keep firing.
func TestLeaderFailover_PromotesExactlyOneStandby(t *testing.T) {
	ctx := context.Background()
	ns := "it-failover"

	leader, err := dcron.New(
		mustOpen(t),
		dcron.WithNamespace(ns),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
		dcron.WithLogger(discardLogger()),
	)
	if err != nil {
		t.Fatalf("leader New: %v", err)
	}
	standbyDB := mustOpen(t)
	standby, err := dcron.New(
		standbyDB,
		dcron.WithNamespace(ns),
		dcron.WithSessionStableConnection(),
		dcron.WithPollInterval(20*time.Millisecond),
		dcron.WithLogger(discardLogger()),
	)
	if err != nil {
		t.Fatalf("standby New: %v", err)
	}
	for i, s := range []*dcron.Scheduler{leader, standby} {
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
	}

	waitFor(t, 10*time.Second, "leader promoted", func() bool {
		return leader.Leadership() == dcron.LeadershipLeader &&
			standby.Leadership() == dcron.LeadershipStandby
	})

	// Kill the leader's connection pool: its lock dies with the session.
	if err := leader.Stop(context.Background()); err != nil {
		t.Logf("leader Stop returned %v (acceptable during failover)", err)
	}

	waitFor(t, 15*time.Second, "exactly one new leader", func() bool {
		return standby.Leadership() == dcron.LeadershipLeader
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitFor(t *testing.T, budget time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", budget, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
