package clock_test

import (
	"container/heap"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
)

func ts(y, mo, d, h, mi int) time.Time {
	return time.Date(y, time.Month(mo), d, h, mi, 0, 0, time.UTC)
}

func tss(y, mo, d, h, mi, s int) time.Time {
	return time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC)
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"", "0 0 * *", "0 0 * * * *", "@every", "@every 0s", "@every -1s",
		"61 * * * *", "* * * * abc", "0 0 32 * *", "* * * *",
	}
	for _, expr := range invalid {
		if _, err := clock.Parse(expr, time.UTC); err == nil {
			t.Errorf("clock.Parse(%q): expected an error", expr)
		}
	}
}

func TestParseInvalidSeconds(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"0 0 * * *", "* * *", "0 0 0 * * * *"} {
		if _, err := clock.ParseSeconds(expr, time.UTC); err == nil {
			t.Errorf("clock.ParseSeconds(%q): expected an error", expr)
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
		{"0 2 * * *", ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 2, 0)},
		{"*/5 * * * *", ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 0, 5)},
		{"0 9-17 * * 1-5", ts(2024, 1, 1, 9, 0), ts(2024, 1, 1, 10, 0)},
		{"0 0 1 1 *", ts(2024, 1, 2, 0, 0), ts(2025, 1, 1, 0, 0)},
		{"30 1 * * 0", ts(2024, 1, 1, 0, 0), ts(2024, 1, 7, 1, 30)},
		{"0 0 * * 1-5", ts(2024, 1, 5, 23, 59), ts(2024, 1, 8, 0, 0)},
		{"*/15 8-17 * * 1-5", ts(2024, 1, 1, 7, 59), ts(2024, 1, 1, 8, 0)},
		{"5/10 * * * *", ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 0, 5)},
	}
	for _, tc := range cases {
		s, err := clock.Parse(tc.expr, time.UTC)
		if err != nil {
			t.Fatalf("clock.Parse(%q): %v", tc.expr, err)
		}
		got := s.Next(tc.from)
		if !got.Equal(tc.want) {
			t.Errorf("Next(%q, %s) = %s; want %s", tc.expr, tc.from, got, tc.want)
		}
	}
}

func TestCronNextMonotonic(t *testing.T) {
	t.Parallel()
	s, err := clock.Parse("*/30 * * * *", time.UTC)
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

func TestParseDescriptors(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"@yearly":   "0 0 1 1 *",
		"@annually": "0 0 1 1 *",
		"@monthly":  "0 0 1 * *",
		"@weekly":   "0 0 * * 0",
		"@daily":    "0 0 * * *",
		"@midnight": "0 0 * * *",
		"@hourly":   "0 * * * *",
	}
	from := ts(2024, 1, 1, 0, 0)
	for desc, expr := range cases {
		s1, err := clock.Parse(desc, time.UTC)
		if err != nil {
			t.Fatalf("clock.Parse(%q): %v", desc, err)
		}
		s2, err := clock.Parse(expr, time.UTC)
		if err != nil {
			t.Fatalf("clock.Parse(%q): %v", expr, err)
		}
		if !s1.Next(from).Equal(s2.Next(from)) {
			t.Errorf("%q and %q fire at different times", desc, expr)
		}
	}
	if _, err := clock.Parse("@bogus", time.UTC); err == nil {
		t.Error(`expected error for "@bogus"`)
	}
}

func TestParseNames(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{
		"0 0 1 jan *",
		"0 0 1 JAN *",
		"0 0 1 january *",
		"0 0 1 Jan *",
		"30 9 * * MON-FRI",
		"0 12 * * mon",
		"0 0 * * Sun",
	} {
		if _, err := clock.Parse(expr, time.UTC); err != nil {
			t.Errorf("clock.Parse(%q): %v", expr, err)
		}
	}
	for name, num := range map[string]string{
		"0 0 1 jan *":      "0 0 1 1 *",
		"0 0 * * mon":      "0 0 * * 1",
		"30 9 * * MON-FRI": "30 9 * * 1-5",
	} {
		from := ts(2024, 1, 1, 0, 0)
		s1, err := clock.Parse(name, time.UTC)
		if err != nil {
			t.Fatalf("clock.Parse(%q): %v", name, err)
		}
		s2, err := clock.Parse(num, time.UTC)
		if err != nil {
			t.Fatalf("clock.Parse(%q): %v", num, err)
		}
		if !s1.Next(from).Equal(s2.Next(from)) {
			t.Errorf("%q and %q fire at different times", name, num)
		}
	}
	for _, bad := range []string{"0 0 1 xyz *", "0 0 * * friyay", "0 0 1 * * 8"} {
		if _, err := clock.Parse(bad, time.UTC); err == nil {
			t.Errorf("clock.Parse(%q): expected error", bad)
		}
	}
}

func TestParseSeconds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		expr string
		from time.Time
		want time.Time
	}{
		{"15 0 2 * * *", tss(2024, 1, 1, 0, 0, 0), tss(2024, 1, 1, 2, 0, 15)},
		{"*/30 * * * * *", tss(2024, 1, 2, 0, 0, 0), tss(2024, 1, 2, 0, 0, 30)},
		{"0 0 0 * * *", tss(2024, 1, 1, 0, 0, 0), tss(2024, 1, 2, 0, 0, 0)},
		{"0 30 2 * jan *", tss(2024, 1, 1, 0, 0, 0), tss(2024, 1, 1, 2, 30, 0)},
	}
	for _, tc := range cases {
		s, err := clock.ParseSeconds(tc.expr, time.UTC)
		if err != nil {
			t.Fatalf("clock.ParseSeconds(%q): %v", tc.expr, err)
		}
		if got := s.Next(tc.from); !got.Equal(tc.want) {
			t.Errorf("clock.ParseSeconds(%q).Next(%s) = %s; want %s", tc.expr, tc.from, got, tc.want)
		}
	}

	d, err := clock.ParseSeconds("@daily", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Next(ts(2024, 1, 1, 0, 0)); !got.Equal(ts(2024, 1, 2, 0, 0)) {
		t.Errorf("clock.ParseSeconds(@daily).Next = %s; want 2024-01-02 00:00", got)
	}
}

func TestCronNextFeb29(t *testing.T) {
	t.Parallel()
	s, err := clock.Parse("0 0 29 2 *", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ from, want time.Time }{
		{ts(2023, 1, 1, 0, 0), ts(2024, 2, 29, 0, 0)},
		{ts(2024, 3, 1, 0, 0), ts(2028, 2, 29, 0, 0)},
		{ts(2025, 1, 1, 0, 0), ts(2028, 2, 29, 0, 0)},
	}
	for _, c := range cases {
		if got := s.Next(c.from); !got.Equal(c.want) {
			t.Errorf("Next(%s) = %s; want %s", c.from, got, c.want)
		}
	}
}

func TestCronNextDST(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata available: %v", err)
	}
	mar10 := time.Date(2024, 3, 10, 0, 0, 0, 0, loc)
	skipped, _ := clock.Parse("0 2 * * *", loc)
	if got := skipped.Next(mar10); !got.Equal(time.Date(2024, 3, 11, 2, 0, 0, 0, loc)) {
		t.Errorf("skipped 02:00 on spring-forward: Next = %s; want 2024-03-11 02:00", got)
	}
	exists, _ := clock.Parse("0 3 * * *", loc)
	if got := exists.Next(mar10); !got.Equal(time.Date(2024, 3, 10, 3, 0, 0, 0, loc)) {
		t.Errorf("03:00 on spring-forward day: Next = %s; want 2024-03-10 03:00", got)
	}
	nov02 := time.Date(2024, 11, 2, 23, 0, 0, 0, loc)
	first, _ := clock.Parse("0 1 * * *", loc)
	if got := first.Next(nov02); !got.Equal(time.Date(2024, 11, 3, 1, 0, 0, 0, loc)) {
		t.Errorf("first 01:00 on fall-back: Next = %s; want 2024-11-03 01:00 EDT", got)
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"0 0 * * *", "0 0 1 1 *", "5/10 * * * *", "@hourly", "@daily",
		"0 0 * * 0", "* * * * *", "*/5 * * * *", "0 0 1 1 1",
	}
	for _, s := range seeds {
		f.Add(s, false)
		f.Add(s, true)
	}
	for _, s := range []string{"@every 3s", "@every 10m", "0 0 29 2 *", "0 0 1 jan *"} {
		f.Add(s, false)
	}
	f.Fuzz(func(t *testing.T, expr string, withSec bool) {
		from := ts(2024, 1, 1, 0, 0)
		var (
			s1, s2 clock.Schedule
			err    error
		)
		if withSec {
			s1, err = clock.ParseSeconds(expr, time.UTC)
		} else {
			s1, err = clock.Parse(expr, time.UTC)
		}
		if err != nil {
			return
		}
		if withSec {
			s2, err = clock.ParseSeconds(expr, time.UTC)
		} else {
			s2, err = clock.Parse(expr, time.UTC)
		}
		if err != nil {
			t.Fatalf("re-parse failed: %v", err)
		}
		if n := s1.Next(from); !n.Equal(s2.Next(from)) {
			t.Errorf("non-deterministic Next(%q): %s vs %s", expr, s1.Next(from), s2.Next(from))
		}
	})
}
