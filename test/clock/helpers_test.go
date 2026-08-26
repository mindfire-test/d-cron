package clock_test

import (
	"time"
)

func ts(year, month, day, hour, minute int) time.Time {
	return time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC)
}

func tss(year, month, day, hour, minute, sec int) time.Time {
	return time.Date(year, time.Month(month), day, hour, minute, sec, 0, time.UTC)
}
