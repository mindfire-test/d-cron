package dcron_test

import (
	"context"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
)

func TestLeadershipThreeState(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if got := s.Leadership(); got != dcron.LeadershipUnknown {
		t.Fatalf("pre-start Leadership = %v; want unknown", got)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return s.Leadership() == dcron.LeadershipLeader }, "leadership to promote")
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHealthCheckNilDBPassthrough(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck with no db: %v", err)
	}
}

func TestJobsReportsRunOutcome(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.Add("ok", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool {
		st := s.Jobs()
		return len(st) == 1 && st[0].LastOutcome == "ok"
	}, "job outcome")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}

func TestJobsReportsRunningFlag(t *testing.T) {
	s := testScheduler(newSchedBackend())
	if err := s.Add("slow", "@every 2ms", func(_ context.Context) error {
		for i := 0; i < 30_000_000; i++ {
			_ = i
		}
		return nil
	}, dcron.WithTimeout(10*time.Minute), dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool {
		st := s.Jobs()
		return len(st) == 1 && st[0].Running
	}, "job running flag")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}
