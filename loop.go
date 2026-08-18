package dcron

import (
	"container/heap"
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
	"github.com/mindfire-test/d-cron/internal/executor"
)

// runLoop is the scheduler's background loop (SDS §4.1): on each wake it
// re-confirms leadership via the elector (never re-acquiring, see elector) and,
// when leader, fires every job whose time has come. The wake interval is the
// poll interval with +/-10% jitter so N replicas do not pound the database in
// lock-step.
func (s *Scheduler) runLoop() {
	defer close(s.done)
	timer := time.NewTimer(s.jitteredPoll())
	defer timer.Stop()
	for {
		now := time.Now().In(s.opts.location)
		_, epoch, err := s.leader.Acquire(s.runCtx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			s.opts.logger.Error("dcron: leadership poll failed", "err", err)
		} else if s.leader.IsLeader() {
			s.fireDue(now, epoch)
		}
		select {
		case <-s.runCtx.Done():
			return
		case <-timer.C:
			timer.Reset(s.jitteredPoll())
		}
	}
}

// fireDue executes every job whose FireAt is at or before now and re-queues
// each for its next fire. Overlap is suppressed for jobs registered with
// WithNoOverlap (SDS §7.3).
func (s *Scheduler) fireDue(now time.Time, epoch int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cd := range s.clk.NextDue(now) {
		j := s.jobs[cd.Name]
		if j == nil {
			continue
		}
		if next := j.sched.Next(cd.FireAt); !next.IsZero() {
			heap.Push(s.clk, &clock.Job{Name: j.name, FireAt: next, Sched: j.sched})
		}
		if !j.overlap && !j.busy.TryLock() {
			s.opts.logger.Warn("dcron: skipping fire while previous run is active", "job", j.name)
			continue
		}
		s.invoke(j, epoch)
	}
}

// invoke dispatches one job run through the executor group with the
// leader-epoch fence token and a default idempotency key (the job name)
// injected into the job's context.
func (s *Scheduler) invoke(j *Job, epoch int64) {
	fn := func(ctx context.Context) error {
		if !j.overlap {
			defer j.busy.Unlock()
		}
		ctx = WithEpoch(ctx, epoch)
		ctx = WithIdempotencyKey(ctx, j.name)
		return j.fn(ctx)
	}
	s.group.Go(executor.Func(fn), j.retry, s.opts.logger)
}

// jitteredPoll returns the poll interval with +/-10% jitter. The floor is 1ms
// so a misconfigured interval cannot spin the loop.
func (s *Scheduler) jitteredPoll() time.Duration {
	d := s.opts.pollInterval
	if d < time.Millisecond {
		d = time.Millisecond
	}
	j := d / 10
	return d - j + time.Duration(rand.Int64N(2*int64(j)))
}
