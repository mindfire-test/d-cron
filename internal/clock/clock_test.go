package clock

import (
	"container/heap"
	"testing"
	"time"
)

func ts(y, mo, d, h, mi int) time.Time {
	return time.Date(y, time.Month(mo), d, h, mi, 0, 0, time.UTC)
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"", "0 0 * *", "0 0 * * * *", "@every", "@every 0s", "@every -1s",
		"61 * * * *", "* * * * abc", "0 0 32 * *", "* * * *",
	}
	for _, expr := range invalid {
		if _, err := Parse(expr, time.UTC); err == nil {
			t.Errorf("Parse(%q): expected an error", expr)
		}
	}
}

func TestCronNext(t *testing.T) {
	t.Parallel()
	cases := []struct {
		expr string
		from time.Time
		want time.Time
	}{
		{"0 2 * * *", ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 2, 0)},          // daily 02:00
		{"*/5 * * * *", ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 0, 5)},        // every 5 min
		{"0 9-17 * * 1-5", ts(2024, 1, 1, 9, 0), ts(2024, 1, 1, 10, 0)},    // weekdays 09-17, Mon
		{"0 0 1 1 *", ts(2024, 1, 2, 0, 0), ts(2025, 1, 1, 0, 0)},          // yearly
		{"30 1 * * 0", ts(2024, 1, 1, 0, 0), ts(2024, 1, 7, 1, 30)},        // next Sunday 01:30
		{"0 0 * * 1-5", ts(2024, 1, 5, 23, 59), ts(2024, 1, 8, 0, 0)},      // Mon-Fri, skip weekend
		{"*/15 8-17 * * 1-5", ts(2024, 1, 1, 7, 59), ts(2024, 1, 1, 8, 0)}, // 15m during 08-17 weekdays
		{"5/10 * * * *", ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 0, 5)},       // 5,15,25..35..55
	}
	for _, tc := range cases {
		s, err := Parse(tc.expr, time.UTC)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.expr, err)
		}
		got := s.Next(tc.from)
		if !got.Equal(tc.want) {
			t.Errorf("Next(%q, %s) = %s; want %s", tc.expr, tc.from, got, tc.want)
		}
	}
}

func TestCronNextMonotonic(t *testing.T) {
	t.Parallel()
	s, err := Parse("*/30 * * * *", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	cur := ts(2024, 1, 1, 0, 0)
	for i := 0; i < 100; i++ {
		prev := cur
		cur = s.Next(cur)
		if !cur.After(prev) {
			t.Fatalf("Next not strictly increasing: %s -> %s", prev, cur)
		}
	}
}

func TestIntervalNext(t *testing.T) {
	t.Parallel()
	s, err := Parse("@every 10m", time.UTC)
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
	q := &Queue{}
	heap.Push(q, &Job{Name: "a", FireAt: ts(2024, 1, 1, 0, 5), Sched: nil})
	heap.Push(q, &Job{Name: "b", FireAt: ts(2024, 1, 1, 0, 3), Sched: nil})
	heap.Push(q, &Job{Name: "c", FireAt: ts(2024, 1, 1, 0, 9), Sched: nil})
	now := ts(2024, 1, 1, 0, 6)
	due := q.NextDue(now)
	if len(due) != 2 {
		t.Fatalf("len(due) = %d; want 2", len(due))
	}
	if due[0].Name != "b" || due[1].Name != "a" {
		t.Errorf("due order = %q, %q; want b, a", due[0].Name, due[1].Name)
	}
	if q.Len() != 1 || q.peek().Name != "c" {
		t.Errorf("remaining queue is wrong: len=%d name=%q", q.Len(), q.peek().Name)
	}
}
