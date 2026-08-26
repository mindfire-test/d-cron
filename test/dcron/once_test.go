package dcron_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
)

func TestAddOnceEvictsAfterFiring(t *testing.T) {
	s := testScheduler(newSchedBackend())
	var fired atomic.Int64
	at := time.Now().Add(500 * time.Millisecond)
	if err := s.AddOnce("wallclock", at, func(context.Context) error {
		fired.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("AddOnce: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return fired.Load() >= 1 }, "once-job to fire")
	time.Sleep(400 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if got := fired.Load(); got != 1 {
		t.Fatalf("once job fired %d times; want exactly 1", got)
	}
}

func TestAddOnceRejectsPastTime(t *testing.T) {
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }
	past := time.Now().Add(-time.Minute)
	if err := s.AddOnce("expired", past, noop); !errors.Is(err, dcron.ErrInvalidSpec) {
		t.Fatalf("AddOnce with past time err = %v; want dcron.ErrInvalidSpec", err)
	}
}

func TestAddOnceDuplicateName(t *testing.T) {
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }
	at := time.Now().Add(time.Hour)
	if err := s.AddOnce("dup", at, noop); err != nil {
		t.Fatalf("AddOnce: %v", err)
	}
	if err := s.AddOnce("dup", at.Add(time.Hour), noop); !errors.Is(err, dcron.ErrJobExists) {
		t.Fatalf("duplicate AddOnce err = %v; want dcron.ErrJobExists", err)
	}
}
