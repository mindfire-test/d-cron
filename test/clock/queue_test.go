package clock_test

import (
	"container/heap"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
)

func TestIntervalNext(t *testing.T) {
	t.Parallel()
	s, err := clock.Parse("@every 10m", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	from := ts(2024, 1, 1, 0, 0)
	got := s.Next(from)
	if !got.Equal(from.Add(10 * time.Minute)) {
		t.Errorf("Next = %s; want %s", got, from.Add(10*time.Minute))
	}
}

func TestQueueNextDue(t *testing.T) {
	t.Parallel()
	q := &clock.Queue{}
	heap.Push(q, &clock.Job{Name: "a", FireAt: ts(2024, 1, 1, 0, 5), Sched: nil})
	heap.Push(q, &clock.Job{Name: "b", FireAt: ts(2024, 1, 1, 0, 3), Sched: nil})
	heap.Push(q, &clock.Job{Name: "c", FireAt: ts(2024, 1, 1, 0, 9), Sched: nil})
	now := ts(2024, 1, 1, 0, 6)
	due := q.NextDue(now)
	if len(due) != 2 {
		t.Fatalf("len(due) = %d; want 2", len(due))
	}
	if due[0].Name != "b" || due[1].Name != "a" {
		t.Errorf("due order = %q, %q; want b, a", due[0].Name, due[1].Name)
	}
	if q.Len() != 1 || q.Peek().Name != "c" {
		t.Errorf("remaining queue is wrong: len=%d name=%q", q.Len(), q.Peek().Name)
	}
}
