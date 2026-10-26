package dcron

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mindfire-test/d-cron/internal/clock"
	"github.com/mindfire-test/d-cron/internal/elector"
	"github.com/mindfire-test/d-cron/internal/executor"
	"github.com/mindfire-test/d-cron/internal/store"
	"github.com/mindfire-test/d-cron/metrics"
)

const (
	defaultPruneTimeout = 15 * time.Second
)

func (s *Scheduler) runLoop() {
	defer close(s.done)
	timer := time.NewTimer(s.jitteredPoll())
	defer timer.Stop()
	wasLeader := false

	for {
		now := time.Now().In(s.opts.location)
		_, electorEpoch, err := s.leader.Acquire(s.loopCtx)
		isLeader := s.leader.IsLeader()

		switch {
		case err != nil && !errors.Is(err, context.Canceled):
			s.opts.logger.Error("dcron: leadership poll failed", "err", err)
			if wasLeader {
				s.onDemotion()
			}
		case isLeader && !wasLeader:
			s.onPromotion(electorEpoch)
		case !isLeader && wasLeader:
			s.onDemotion()
		}
		wasLeader = isLeader

		if isLeader {
			s.mu.Lock()
			epoch := s.currentEpoch
			s.mu.Unlock()
			if s.store != nil && epoch == 0 {
				epochCtx, cancel := context.WithTimeout(s.loopCtx, s.opts.dbTimeout)
				dbEpoch, err := s.store.IncrementEpoch(epochCtx, s.opts.namespace, s.opts.instance)
				cancel()
				if err == nil {
					s.mu.Lock()
					s.currentEpoch = dbEpoch
					epoch = dbEpoch
					s.mu.Unlock()
					if el, ok := s.leader.(*elector.Elector); ok {
						el.SetEpoch(dbEpoch)
					}
					s.opts.logger.Info("dcron: leader epoch incremented on retry", "epoch", dbEpoch)
					s.reconcileJobStates(dbEpoch)
				}
			}
			if s.store == nil || epoch > 0 {
				s.fireDue(now, epoch)
				s.pruneHistory(now)
			}
		}

		select {
		case <-s.loopCtx.Done():
			return
		case <-timer.C:
			timer.Reset(s.jitteredPoll())
		}
	}
}

func (s *Scheduler) onPromotion(electorEpoch int64) {
	epoch := electorEpoch
	if s.store != nil {
		epochCtx, cancel := context.WithTimeout(s.loopCtx, s.opts.dbTimeout)
		dbEpoch, err := s.store.IncrementEpoch(epochCtx, s.opts.namespace, s.opts.instance)
		cancel()
		if err == nil {
			epoch = dbEpoch
			s.opts.logger.Info("dcron: leader epoch incremented", "epoch", dbEpoch)
		} else {
			epoch = 0
			s.opts.logger.Warn("dcron: leader epoch increment failed", "err", err)
		}
	}

	s.mu.Lock()
	s.termCancel()
	s.termCtx, s.termCancel = context.WithCancel(s.runCtx)
	s.currentEpoch = epoch
	s.mu.Unlock()

	if el, ok := s.leader.(*elector.Elector); ok && epoch > 0 {
		el.SetEpoch(epoch)
	}

	s.opts.rec.SetLeader(s.opts.instance, true)
	s.opts.rec.LeaderTransition(s.opts.instance)

	if epoch > 0 {
		s.reconcileJobStates(epoch)
		s.catchUpMissedRuns(epoch)
	}
}

func (s *Scheduler) catchUpMissedRuns(epoch int64) {
	if s.store == nil || epoch <= 0 {
		return
	}
	s.mu.Lock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		j.statusMu.Lock()
		paused := j.paused
		j.statusMu.Unlock()
		if !paused && j.missedPolicy == MissedCatchUp {
			jobs = append(jobs, j)
		}
	}
	s.mu.Unlock()

	if len(jobs) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(s.loopCtx, s.opts.dbTimeout)
	defer cancel()

	now := time.Now().In(s.opts.location)
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		recent, err := s.store.Recent(ctx, s.opts.namespace, j.name, 1)
		if err != nil {
			s.opts.logger.Warn("dcron: failed to query recent executions for catchup", "job", j.name, "err", err)
			continue
		}
		if len(recent) == 0 {
			continue
		}
		lastScheduled := recent[0].ScheduledAt
		maxLookback := j.maxLookback
		if maxLookback <= 0 {
			maxLookback = 24 * time.Hour
		}
		cutoff := now.Add(-maxLookback)
		if lastScheduled.Before(cutoff) {
			lastScheduled = cutoff
		}

		maxRuns := j.maxCatchUp
		if maxRuns <= 0 {
			maxRuns = 10
		}

		var missed []time.Time
		curr := lastScheduled
		for {
			next := j.sched.Next(curr)
			if next.IsZero() || !next.Before(now) {
				break
			}
			missed = append(missed, next)
			if len(missed) >= maxRuns {
				break
			}
			curr = next
		}

		for _, fireAt := range missed {
			s.mu.Lock()
			if !s.leader.IsLeader() || s.currentEpoch != epoch {
				s.mu.Unlock()
				return
			}
			if !j.overlap && !j.busy.TryLock() {
				s.mu.Unlock()
				s.opts.logger.Warn("dcron: skipping catchup fire while previous run is active", "job", j.name)
				continue
			}
			s.invoke(j, epoch, fireAt)
			s.mu.Unlock()
		}
	}
}

func (s *Scheduler) onDemotion() {
	s.mu.Lock()
	s.termCancel()
	s.termCtx, s.termCancel = context.WithCancel(s.runCtx)
	s.termCancel()
	s.currentEpoch = 0
	for _, j := range s.jobs {
		j.statusMu.Lock()
		j.queuedRun = false
		j.statusMu.Unlock()
	}
	s.mu.Unlock()

	s.leader.FinalizeDemotion()
	s.opts.rec.SetLeader(s.opts.instance, false)
	s.opts.rec.LeaderTransition(s.opts.instance)
}

const pruneInterval = time.Minute

func (s *Scheduler) pruneHistory(now time.Time) {
	if s.store == nil || s.opts.retention <= 0 {
		return
	}
	if now.Sub(s.lastPrune) < pruneInterval {
		return
	}
	s.lastPrune = now

	// Ensure at most one background pruning operation runs at any time.
	if !s.pruneRunning.CompareAndSwap(false, true) {
		s.opts.logger.Debug("dcron: skipping history prune; previous prune operation still in progress")
		return
	}

	timeout := defaultPruneTimeout
	if s.opts.dbTimeout > 0 {
		timeout = s.opts.dbTimeout
	}

	s.pruneWg.Add(1)
	go func() {
		defer s.pruneWg.Done()
		defer s.pruneRunning.Store(false)

		pruneCtx, cancel := context.WithTimeout(s.runCtx, timeout)
		defer cancel()

		n, err := s.store.Prune(pruneCtx, s.opts.namespace, s.opts.retention)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				s.opts.logger.Warn("dcron: history prune failed", "err", err)
			}
			return
		}
		if n > 0 {
			s.opts.logger.Info("dcron: pruned history", "rows", n)
		}
	}()
}

type dueJobFiring struct {
	job    *Job
	fireAt time.Time
}

// fireDue evaluates ticks on the Leader replica using the location resolved at construction.
func (s *Scheduler) fireDue(now time.Time, epoch int64) {
	var toFire []dueJobFiring

	s.mu.Lock()
	for _, cd := range s.clk.NextDue(now) {
		j := s.jobs[cd.Name]
		if j == nil {
			continue
		}
		if _, isSinceSuccess := j.sched.(clock.SinceSuccessSchedule); isSinceSuccess {
			j.nextRun = time.Time{}
		} else if next := j.sched.Next(cd.FireAt); !next.IsZero() {
			j.nextRun = next
			heap.Push(s.clk, &clock.Job{Name: j.name, FireAt: next, Sched: j.sched})
		} else {
			j.nextRun = time.Time{}
		}
		j.statusMu.Lock()
		isPaused := j.paused
		j.statusMu.Unlock()
		if isPaused {
			s.opts.logger.Info("dcron: skipping fire for paused job", "job", j.name)
			continue
		}
		if !j.overlap && !j.busy.TryLock() {
			if j.overlapPolicy == OverlapQueue {
				j.statusMu.Lock()
				j.queuedRun = true
				j.statusMu.Unlock()
				s.opts.logger.Info("dcron: queuing fire while previous run is active", "job", j.name)
			} else {
				s.opts.logger.Warn("dcron: skipping fire while previous run is active", "job", j.name)
				s.opts.rec.MissedRun(j.name)
			}
			continue
		}
		toFire = append(toFire, dueJobFiring{job: j, fireAt: cd.FireAt})
	}
	s.mu.Unlock()

	for _, df := range toFire {
		s.invoke(df.job, epoch, df.fireAt)
	}
}

/* the number of the functiona*/
func (s *Scheduler) invoke(j *Job, epoch int64, fireAt time.Time) {
	if s.termCtx == nil || s.termCtx.Err() != nil {
		if !j.overlap {
			j.busy.Unlock()
		}
		return
	}
	started := time.Now()
	var rowID int64
	var recordOnce sync.Once

	var endSpan func(outcome string, err error)
	spanCtx := s.termCtx
	if s.opts.tracer != nil {
		spanCtx, endSpan = s.opts.tracer.StartSpan(s.termCtx, j.name, fireAt, 1, epoch)
	} else {
		endSpan = func(string, error) {}
	}

	fn := func(ctx context.Context) error {
		ctx = WithNamespaceKey(ctx, s.opts.namespace)
		ctx = WithEpoch(ctx, epoch)
		ctx = WithIdempotencyKey(ctx, DeriveIdempotencyKey(s.opts.namespace, j.name, fireAt))
		recordOnce.Do(func() {
			if s.store == nil {
				return
			}
			recordCtx, cancel := context.WithTimeout(ctx, s.opts.dbTimeout)
			row, err := s.store.Record(recordCtx, store.Execution{
				Namespace:   s.opts.namespace,
				JobName:     j.name,
				ScheduledAt: fireAt,
				StartedAt:   started,
				Status:      store.StatusRunning,
				Attempt:     1,
				InstanceID:  s.opts.instance,
				LeaderEpoch: epoch,
			})
			cancel()
			if err != nil {
				s.opts.logger.Warn("dcron: history record failed", "job", j.name, "err", err)
				return
			}
			rowID = row
		})
		return j.fn(ctx)
	}

	j.statusMu.Lock()
	j.running = true
	j.lastError = ""
	j.statusMu.Unlock()
	s.opts.rec.JobStarted(j.name)

	capturedTerm := s.termCtx

	onComplete := func(res executor.Result) {
		endSpan(res.Outcome.String(), res.Error)

		j.statusMu.Lock()
		j.running = false
		j.lastOutcome = res.Outcome.String()
		j.lastDuration = res.Duration
		if res.Error != nil {
			j.lastError = res.Error.Error()
		}
		hasQueued := j.queuedRun
		j.queuedRun = false
		j.statusMu.Unlock()

		s.opts.rec.JobFinished(j.name, outcomeToMetric(res.Outcome), res.Duration, res.Outcome == executor.OutcomeOK)
		if s.store != nil && rowID != 0 {
			finishCtx, cancel := context.WithTimeout(context.Background(), s.opts.dbTimeout)
			rows, err := s.store.Finish(finishCtx, rowID, store.Execution{
				Namespace:  s.opts.namespace,
				Status:     historyStatus(res.Outcome),
				FinishedAt: started.Add(res.Duration),
				DurationMs: res.Duration.Milliseconds(),
				Error:      errorString(res.Error),
				Attempt:    res.Attempts,
			})
			cancel()
			if err != nil {
				s.opts.logger.Warn("dcron: history finish failed", "job", j.name, "err", err)
			} else if rows == 0 {
				s.opts.logger.Warn("dcron: fenced write detected on finish", "job", j.name)
				s.opts.rec.FencedWrite()
			}
		}
		s.fireHooks(res)

		if _, isSinceSuccess := j.sched.(clock.SinceSuccessSchedule); isSinceSuccess && res.Outcome == executor.OutcomeOK {
			s.mu.Lock()
			if s.started && s.leader.IsLeader() {
				next := j.sched.Next(time.Now().In(s.opts.location))
				j.nextRun = next
				heap.Push(s.clk, &clock.Job{Name: j.name, FireAt: next, Sched: j.sched})
			}
			s.mu.Unlock()
		}

		if !hasQueued {
			if !j.overlap {
				j.busy.Unlock()
			}
			return
		}

		go func(termCtx context.Context) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if !s.started || !s.leader.IsLeader() || s.termCtx != termCtx || termCtx.Err() != nil {
				if !j.overlap {
					j.busy.Unlock()
				}
				s.opts.logger.Info("dcron: skipping queued run after leadership transition", "job", j.name)
				return
			}
			currentEpoch := s.currentEpoch
			s.invoke(j, currentEpoch, time.Now().In(s.opts.location))
		}(capturedTerm)
	}

	s.group.Go(spanCtx, j.name, executor.Func(fn), j.retry, s.opts.logger, onComplete)
}

func historyStatus(o executor.Outcome) store.Status {
	switch o {
	case executor.OutcomeOK:
		return store.StatusSuccess
	case executor.OutcomeFailed:
		return store.StatusFailed
	case executor.OutcomePanicked:
		return store.StatusPanicked
	case executor.OutcomeTimedOut:
		return store.StatusTimeout
	default:
		return store.StatusSkipped
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// DeriveIdempotencyKey returns the deterministic idempotency key issued to a job firing for name in namespace at fireAt.
func DeriveIdempotencyKey(namespace, name string, fireAt time.Time) string {
	s := "d-cron:v1:" + namespace + ":" + name + ":" + fireAt.UTC().Format(time.RFC3339)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// jitteredPoll calculates the standby polling interval with randomized jitter.
func (s *Scheduler) jitteredPoll() time.Duration {
	d := s.opts.pollInterval
	if d < time.Millisecond {
		d = time.Millisecond
	}
	j := d / 5
	return d - j + time.Duration(rand.Int64N(2*int64(j)))
}

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
