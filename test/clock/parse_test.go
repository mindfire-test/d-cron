package clock_test

import (
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
)

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
