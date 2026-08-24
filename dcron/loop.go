package dcron

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
	"github.com/mindfire-test/d-cron/internal/executor"
	"github.com/mindfire-test/d-cron/metrics"
)

// runLoop is the scheduler's background loop (SDS §4.1): on each wake it
// re-confirms leadership via the elector (never re-acquiring, see elector) and,
// when leader, fires every job whose time has come. The wake interval is the
// poll interval with ±20% jitter (issue #9) so N replicas do not pound the
// database in lock-step.
//
// Leadership transitions are tracked here: on promotion the leadership term
// context is refreshed, and on demotion it is cancelled so in-flight jobs (and
// their retries) stop immediately (FR-307) — a demoted leader must not keep
// working a fire time that a successor may also be working.
func (s *Scheduler) runLoop() {
	defer close(s.done)
	timer := time.NewTimer(s.jitteredPoll())
	defer timer.Stop()
	wasLeader := false

	for {
		now := time.Now().In(s.opts.location)
		_, epoch, err := s.leader.Acquire(s.runCtx)
		isLeader := s.leader.IsLeader()

		switch {
		case err != nil && !errors.Is(err, context.Canceled):
			// Transient DB failure (NFR-202): log, never crash the host. Treat
			// as a potential loss of leadership so stale work aborts.
			s.opts.logger.Error("dcron: leadership poll failed", "err", err)
			if wasLeader {
				s.onDemotion()
			}
		case isLeader && !wasLeader:
			s.onPromotion()
		case !isLeader && wasLeader:
			s.onDemotion()
		}
		wasLeader = isLeader

		if isLeader {
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

// onPromotion refreshes the leadership term context and flips the leader gauge.
// Runs on the loop goroutine.
func (s *Scheduler) onPromotion() {
	s.termCancel()
	s.termCtx, s.termCancel = context.WithCancel(s.runCtx)
	s.opts.rec.SetLeader(s.opts.instance, true)
	s.opts.rec.LeaderTransition(s.opts.instance)
}

// onDemotion aborts in-flight work for the finished leadership term, settles
// the elector back to standby, and clears the leader gauge. Runs on the loop
// goroutine.
func (s *Scheduler) onDemotion() {
	s.termCancel()
	s.leader.FinalizeDemotion()
	s.opts.rec.SetLeader(s.opts.instance, false)
	s.opts.rec.LeaderTransition(s.opts.instance)
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
			j.nextRun = next
			heap.Push(s.clk, &clock.Job{Name: j.name, FireAt: next, Sched: j.sched})
		} else {
			j.nextRun = time.Time{}
		}
		if !j.overlap && !j.busy.TryLock() {
			s.opts.logger.Warn("dcron: skipping fire while previous run is active", "job", j.name)
			continue
		}
		s.invoke(j, epoch, cd.FireAt)
	}
}

// invoke dispatches one job run through the executor group under the leadership
// term context (cancelled on demotion and on shutdown), injecting the leader
// epoch fence token and the deterministic fire-time idempotency key (issue #21,
// SDS §5.4). It records job status on the Job so Jobs() can report it (#37)
// and hands the final Result to any registered hooks (#39).
func (s *Scheduler) invoke(j *Job, epoch int64, fireAt time.Time) {
	fn := func(ctx context.Context) error {
		if !j.overlap {
			defer j.busy.Unlock()
		}
		ctx = WithEpoch(ctx, epoch)
		ctx = WithIdempotencyKey(ctx, deriveIdempotencyKey(s.opts.namespace, j.name, fireAt))
		return j.fn(ctx)
	}
	// Snapshot status under statusMu (guarded separately from s.mu since the
	// callback runs on the executor goroutine, not the loop goroutine).
	j.statusMu.Lock()
	j.running = true
	j.lastError = ""
	j.statusMu.Unlock()
	s.opts.rec.JobStarted(j.name)

	onComplete := func(res executor.Result) {
		j.statusMu.Lock()
		j.running = false
		j.lastOutcome = res.Outcome.String()
		j.lastDuration = res.Duration
		if res.Error != nil {
			j.lastError = res.Error.Error()
		}
		j.statusMu.Unlock()
		s.opts.rec.JobFinished(j.name, outcomeToMetric(res.Outcome), res.Duration, res.Outcome == executor.OutcomeOK)
		s.fireHooks(res)
	}
	s.group.Go(s.termCtx, j.name, executor.Func(fn), j.retry, s.opts.logger, onComplete)
}

// deriveIdempotencyKey derives the deterministic key for one execution of job in
// namespace at fireAt (SDS §5.4, issue #21):
//
//	sha256("d-cron:v1:" + namespace + ":" + jobName + ":" + fireTime.UTC().Format(RFC3339))
//
// Two replicas working the same fire time produce the same key, so a job can
// deduplicate downstream effects (e.g. a payment provider's idempotency header).
func deriveIdempotencyKey(namespace, name string, fireAt time.Time) string {
	s := "d-cron:v1:" + namespace + ":" + name + ":" + fireAt.UTC().Format(time.RFC3339)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// jitteredPoll returns the poll interval with ±20% jitter. The floor is 1ms so
// a misconfigured interval cannot spin the loop.
func (s *Scheduler) jitteredPoll() time.Duration {
	d := s.opts.pollInterval
	if d < time.Millisecond {
		d = time.Millisecond
	}
	j := d / 5
	return d - j + time.Duration(rand.Int64N(2*int64(j)))
}

// outcomeToMetric maps an executor Outcome onto the metrics package's Outcome
// enum (issue #36). The metrics labels use "success"|"failed"|"panicked"|
// "timeout"|"canceled" per SDS §11.
func outcomeToMetric(o executor.Outcome) metrics.Outcome {
	switch o {
	case executor.OutcomeOK:
		return metrics.OutcomeOK
	case executor.OutcomeFailed:
		return metrics.OutcomeFailed
	case executor.OutcomePanicked:
		return metrics.OutcomePanicked
	case executor.OutcomeTimedOut:
		return metrics.OutcomeTimedOut
	case executor.OutcomeCanceled:
		return metrics.OutcomeCanceled
	default:
		return metrics.OutcomeUnknown
	}
}
