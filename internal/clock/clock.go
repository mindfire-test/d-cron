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
// Supported forms:
//
//   - "@every <duration>", e.g. "@every 3s" or "@every 10m".
//   - A 5-field cron expression: minute hour day-of-month month day-of-week,
//     e.g. "0 2 * * *".
//   - Descriptors: @yearly (alias @annually), @monthly, @weekly, @daily
//     (alias @midnight), @hourly.
//
// Month and day-of-week fields accept names (JAN..DEC, SUN..SAT,
// case-insensitive, full or 3-letter). loc is used to interpret cron field
// boundaries; time.UTC is substituted when nil. 6-field expressions with a
// leading seconds field are parsed by ParseSeconds (issue #15, FR-212).
func Parse(expr string, loc *time.Location) (Schedule, error) {
	if loc == nil {
		loc = time.UTC
	}
	if strings.HasPrefix(expr, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expr, "@every ")))
		if err != nil || d <= 0 {
			return nil, ErrInvalidSchedule
		}
		return IntervalSchedule{d: d, loc: loc}, nil
	}
	if expr == "@every" {
		return nil, ErrInvalidSchedule
	}
	if strings.HasPrefix(expr, "@") {
		expanded, ok := descriptors[expr]
		if !ok {
			return nil, ErrInvalidSchedule
		}
		expr = expanded
	}
	return parseCron(expr, loc)
}

// ParseSeconds interprets a schedule expression with a leading seconds field
// (6-field cron: second minute hour day-of-month month day-of-week). The
// "@every", descriptor, and aliases behave exactly as in Parse. The seconds
// field accepts 0-59, lists, ranges, and steps (issue #15, FR-212).
func ParseSeconds(expr string, loc *time.Location) (Schedule, error) {
	if loc == nil {
		loc = time.UTC
	}
	if strings.HasPrefix(expr, "@") {
		return Parse(expr, loc)
	}
	return parseCronSeconds(expr, loc)
}

// descriptors maps the named schedules to their equivalent 5-field cron
// expressions (issue #15). @yearly and @annually, @daily and @midnight are
// aliases.
var descriptors = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
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

// OnceSchedule fires at one instant and never again: Next returns at for the
// first query strictly before it, and the zero time afterwards so the heap
// evicts the entry (issue #33, FR-209). The zero time is the established
// "never again" signal (SDS §4 table row 3).
type OnceSchedule struct {
	at  time.Time
	loc *time.Location
}

// NewOnce returns a OnceSchedule that fires once at at, interpreted in loc
// (UTC when nil).
func NewOnce(at time.Time, loc *time.Location) OnceSchedule {
	if loc == nil {
		loc = time.UTC
	}
	return OnceSchedule{at: at.In(loc), loc: loc}
}

// Next implements Schedule: at for any t strictly before the fire instant,
// the zero time on or after it.
func (s OnceSchedule) Next(t time.Time) time.Time {
	now := t.In(s.loc)
	if !s.at.After(now) {
		return time.Time{}
	}
	return s.at
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
	minSecond = 0
	maxSecond = 59
)

// CronSchedule is a vixie-style cron schedule: 5 fields by default
// (minute hour dom month dow), or 6 with a leading seconds field when parsed
// by ParseSeconds (issue #15, FR-212).
type CronSchedule struct {
	minute, hour, dom, month, dow uint64
	sec                           uint64
	withSec                       bool
	loc                           *time.Location
}

// searchWindow is how far Next may scan (in days): one full leap cycle, so a
// schedule like "0 0 29 2 *" always finds its next February 29.
const searchWindowDays = 1464

// Next implements Schedule by scanning field-by-field. A valid cron expression
// always matches within a leap cycle, so the bounded window cannot overflow.
func (c CronSchedule) Next(t time.Time) time.Time {
	if c.withSec {
		cur := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, c.loc).Add(time.Second)
		for i := 0; i < searchWindowDays*24*3600; i++ {
			if c.matches(cur) {
				return cur
			}
			cur = cur.Add(time.Second)
		}
		return time.Time{}
	}
	cur := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, c.loc).Add(time.Minute)
	for i := 0; i < searchWindowDays*24*60; i++ {
		if c.matches(cur) {
			return cur
		}
		cur = cur.Add(time.Minute)
	}
	return time.Time{}
}

func (c CronSchedule) matches(t time.Time) bool {
	if c.withSec && c.sec&(1<<uint(t.Second())) == 0 {
		return false
	}
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

// parseCron parses a 5-field vixie-style cron expression (minute hour dom
// month dow). Month and day-of-week fields accept names (issue #15).
func parseCron(expr string, loc *time.Location) (CronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return CronSchedule{}, ErrInvalidSchedule
	}
	minute, err := parseField(fields[0], minMinute, maxMinute, nil, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	hour, err := parseField(fields[1], minHour, maxHour, nil, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	dom, err := parseField(fields[2], minDOM, maxDOM, nil, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	month, err := parseField(fields[3], minMonth, maxMonth, monthNames, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	dow, err := parseField(fields[4], minDOW, maxDOW, dowNames, normalizeDOW)
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

// parseCronSeconds parses a 6-field vixie-style cron expression (second minute
// hour dom month dow). The seconds field is numeric only; month and day-of-week
// still accept names (issue #15, FR-212).
func parseCronSeconds(expr string, loc *time.Location) (CronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 6 {
		return CronSchedule{}, ErrInvalidSchedule
	}
	sec, err := parseField(fields[0], minSecond, maxSecond, nil, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	minute, err := parseField(fields[1], minMinute, maxMinute, nil, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	hour, err := parseField(fields[2], minHour, maxHour, nil, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	dom, err := parseField(fields[3], minDOM, maxDOM, nil, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	month, err := parseField(fields[4], minMonth, maxMonth, monthNames, nil)
	if err != nil {
		return CronSchedule{}, err
	}
	dow, err := parseField(fields[5], minDOW, maxDOW, dowNames, normalizeDOW)
	if err != nil {
		return CronSchedule{}, err
	}
	return CronSchedule{
		sec:     sec,
		minute:  minute,
		hour:    hour,
		dom:     dom,
		month:   month,
		dow:     dow,
		withSec: true,
		loc:     loc,
	}, nil
}

// normalizeDOW maps 7 (a common alias for Sunday) to 0.
func normalizeDOW(v int) int {
	if v == 7 {
		return 0
	}
	return v
}
