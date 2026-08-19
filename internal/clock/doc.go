// Package clock provides the scheduling primitives: a min-heap of pending
// runs, a cron expression parser, and the schedule implementations.
//
// The package is deliberately database-free so it can be unit tested in
// isolation; the elector owns leadership and the clock owns "when". See SDS §4.
package clock
