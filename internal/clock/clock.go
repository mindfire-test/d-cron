// Package clock provides the scheduling primitives shared by the scheduler:
// a min-heap of pending runs, a 5-field cron expression parser, and the
// schedule implementations (cron and interval).
//
// The package is deliberately database-free so it can be unit tested in
// isolation; the elector owns leadership and the clock owns "when". See
// SDS §4.
package clock

import (
	"errors"
	"strings"
	"time"
)

// ErrInvalidSchedule is returned by Parse when expr is not a supported
// schedule expression.
var ErrInvalidSchedule = errors.New("clock: invalid schedule expression")

// Schedule predicts when a job fires.
type Schedule interface {
	// Next returns the next time strictly after t at which the schedule
	// fires, in the location the schedule was parsed with. The zero time is
	// returned when no future firing fits within the search window.
	Next(t time.Time) time.Time
}

// Parse interprets a schedule expression.
//
// Two forms are supported:
//
//   - "@every <duration>", e.g. "@every 3s" or "@every 10m".
//   - A 5-field cron expression: minute hour day-of-month month day-of-week,
//     e.g. "0 2 * * *".
//
// loc is used to interpret cron field boundaries; time.UTC is substituted
// when nil.
func Parse(expr string, loc *time.Location) (Schedule, error) {
	if loc == nil {
		loc = time.UTC
	}
	switch {
	case strings.HasPrefix(expr, "@every "):
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expr, "@every ")))
		if err != nil || d <= 0 {
			return nil, ErrInvalidSchedule
		}
		return IntervalSchedule{d: d, loc: loc}, nil
	case expr == "@every":
		return nil, ErrInvalidSchedule
	default:
		return parseCron(expr, loc)
	}
}

// IntervalSchedule fires at a fixed duration.
type IntervalSchedule struct {
	d   time.Duration
	loc *time.Location
}

// Next implements Schedule.
func (s IntervalSchedule) Next(t time.Time) time.Time {
	return t.In(s.loc).Add(s.d)
}

// Cron field bounds.
const (
	minMinute = 0
	maxMinute = 59
	minHour   = 0
	maxHour   = 23
	minDOM    = 1
	maxDOM    = 31
	minMonth  = 1
	maxMonth  = 12
	minDOW    = 0
	maxDOW    = 6
)

// CronSchedule is a 5-field vixie-style cron schedule
// (minute hour dom month dow).
type CronSchedule struct {
	minute, hour, dom, month, dow uint64
	loc                           *time.Location
}

// Next implements Schedule by scanning minute-by-minute. A valid cron
// expression always matches within a year, so the 400-day window cannot
// overflow in practice.
func (c CronSchedule) Next(t time.Time) time.Time {
	cur := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, c.loc).Add(time.Minute)
	for i := 0; i < 576000; i++ {
		if c.matches(cur) {
			return cur
		}
		cur = cur.Add(time.Minute)
	}
	return time.Time{}
}

func (c CronSchedule) matches(t time.Time) bool {
	if c.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	if c.dom&(1<<uint(t.Day())) == 0 {
		return false
	}
	if c.dow&(1<<uint(t.Weekday())) == 0 {
		return false
	}
	if c.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if c.minute&(1<<t.Minute()) == 0 {
		return false
	}
	return true
}

// parseCron parses a 5-field vixie-style cron expression.
func parseCron(expr string, loc *time.Location) (CronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return CronSchedule{}, ErrInvalidSchedule
	}
	minute, err := parseField(fields[0], minMinute, maxMinute, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	hour, err := parseField(fields[1], minHour, maxHour, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	dom, err := parseField(fields[2], minDOM, maxDOM, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	month, err := parseField(fields[3], minMonth, maxMonth, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	dow, err := parseField(fields[4], minDOW, maxDOW, normalizeDOW)
	if err != nil {
		return CronSchedule{}, err
	}
	return CronSchedule{
		minute: minute,
		hour:   hour,
		dom:    dom,
		month:  month,
		dow:    dow,
		loc:    loc,
	}, nil
}

// normalizeDOW maps 7 (a common alias for Sunday) to 0.
func normalizeDOW(v int) int {
	if v == 7 {
		return 0
	}
	return v
}
