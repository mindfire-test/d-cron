package clock_test

import (
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
)

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
