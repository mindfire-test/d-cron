package executor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/internal/executor"
)

func TestGroupWaitWaitsForMembers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := executor.NewGroup()
	started := make(chan struct{})
	done := make(chan struct{})
	g.Go(ctx, "test",
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(done)
			return ctx.Err()
		}, executor.Retry{Attempts: 1}, nil)
	<-started
	cancel()
	if err := g.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job was not canceled when its context was canceled")
	}
}

func TestGroupWaitBoundedByDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := executor.NewGroup()
	started := make(chan struct{})
	g.Go(ctx, "test",
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			time.Sleep(40 * time.Millisecond)
			return ctx.Err()
		}, executor.Retry{Attempts: 1}, nil)
	<-started
	cancel()
	drain, dcancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer dcancel()
	err := g.Wait(drain)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait=%v, want DeadlineExceeded", err)
	}
}
