package clock

import (
	"container/heap"
	"time"
)

// Job is a pending firing: a registered schedule awaiting its next fire.
type Job struct {
	Name   string
	FireAt time.Time
	Sched  Schedule
}

// Queue is a min-heap of pending jobs ordered by FireAt.
// Only the leader runs this min-heap clock loop; standby replicas do not.
type Queue []*Job

// Len, Less, Swap implement heap.Interface.
func (q Queue) Len() int           { return len(q) }
func (q Queue) Less(i, j int) bool { return q[i].FireAt.Before(q[j].FireAt) }
func (q Queue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }

// Push implements heap.Interface.
func (q *Queue) Push(x any) {
	*q = append(*q, x.(*Job))
}

// Pop implements heap.Interface.
func (q *Queue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return item
}

// NextDue removes and returns every job whose FireAt is at or before now,
// in fire order. The heap invariant must hold on entry (call heap.Init
// once, or maintain it via Push/Pop).
func (q *Queue) NextDue(now time.Time) []*Job {
	var due []*Job
	for q.Len() > 0 && !q.Peek().FireAt.After(now) {
		due = append(due, heap.Pop(q).(*Job))
	}
	return due
}

// Peek returns the earliest-due job without removing it; it panics on an empty
// queue like the other slice-indexing paths.
func (q *Queue) Peek() *Job { return (*q)[0] }

// Realign re-computes the next fire time for every job in the queue using now and the job's schedule,
// discarding jobs whose schedule returns a zero time (e.g. expired one-time jobs).
// The heap invariant is restored via heap.Init.
func (q *Queue) Realign(now time.Time) {
	filtered := (*q)[:0]
	for _, j := range *q {
		if j.Sched == nil {
			continue
		}
		next := j.Sched.Next(now)
		if !next.IsZero() {
			j.FireAt = next
			filtered = append(filtered, j)
		}
	}
	*q = filtered
	heap.Init(q)
}
