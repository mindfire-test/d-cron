// Package clock provides the scheduling primitives: a min-heap of pending
// runs, a cron expression parser (5- or 6-field, the latter via ParseSeconds),
// and the schedule implementations (cron and interval).
//
// The package is deliberately database-free so it can be unit tested in
// isolation; the elector owns leadership and the clock owns "when". See SDS §4.
package clock
