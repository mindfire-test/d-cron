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
	"github.com/mindfire-test/d-cron/internal/executor"
	"github.com/mindfire-test/d-cron/internal/store"
	"github.com/mindfire-test/d-cron/metrics"
)

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
			s.pruneHistory(now)
		}

		select {
		case <-s.runCtx.Done():
			return
		case <-timer.C:
			timer.Reset(s.jitteredPoll())
		}
	}
}

func (s *Scheduler) onPromotion() {
	s.termCancel()
	s.termCtx, s.termCancel = context.WithCancel(s.runCtx)
	s.opts.rec.SetLeader(s.opts.instance, true)
	s.opts.rec.LeaderTransition(s.opts.instance)
	if s.store != nil {
		if epoch, err := s.store.IncrementEpoch(s.runCtx, s.opts.namespace, s.opts.instance); err == nil {
			s.opts.logger.Info("dcron: leader epoch incremented", "epoch", epoch)
		} else {
			s.opts.logger.Warn("dcron: leader epoch increment failed", "err", err)
		}
	}
}

func (s *Scheduler) onDemotion() {
	s.termCancel()
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
	n, err := s.store.Prune(context.Background(), s.opts.namespace, s.opts.retention)
	if err != nil {
		s.opts.logger.Warn("dcron: history prune failed", "err", err)
		return
	}
	if n > 0 {
		s.opts.logger.Info("dcron: pruned history", "rows", n)
	}
}

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

func (s *Scheduler) invoke(j *Job, epoch int64, fireAt time.Time) {
	started := time.Now()
	var rowID int64
	var recordOnce sync.Once

	fn := func(ctx context.Context) error {
		if !j.overlap {
			defer j.busy.Unlock()
		}
		ctx = WithEpoch(ctx, epoch)
		ctx = WithIdempotencyKey(ctx, DeriveIdempotencyKey(s.opts.namespace, j.name, fireAt))
		recordOnce.Do(func() {
			if s.store == nil {
				return
			}
			row, err := s.store.Record(context.Background(), store.Execution{
				Namespace:   s.opts.namespace,
				JobName:     j.name,
				ScheduledAt: fireAt,
				StartedAt:   started,
				Status:      store.StatusRunning,
				Attempt:     1,
				InstanceID:  s.opts.instance,
				LeaderEpoch: epoch,
			})
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
		if s.store != nil && rowID != 0 {
			rows, err := s.store.Finish(context.Background(), rowID, store.Execution{
				Namespace:  s.opts.namespace,
				Status:     historyStatus(res.Outcome),
				FinishedAt: started.Add(res.Duration),
				DurationMs: res.Duration.Milliseconds(),
				Error:      errorString(res.Error),
				Attempt:    res.Attempts,
			})
			if err != nil {
				s.opts.logger.Warn("dcron: history finish failed", "job", j.name, "err", err)
			} else if rows == 0 {
				s.opts.logger.Warn("dcron: fenced write detected on finish", "job", j.name)
				s.opts.rec.FencedWrite()
			}
		}
		s.fireHooks(res)
	}
	s.group.Go(s.termCtx, j.name, executor.Func(fn), j.retry, s.opts.logger, onComplete)
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

// DeriveIdempotencyKey returns the deterministic idempotency key issued to a
// job firing for name in namespace at fireAt (SDS §5.4, issue #21):
//
//	sha256("d-cron:v1:" + namespace + ":" + jobName + ":" + fireTime.UTC().Format(RFC3339))
//
// Two replicas working the same fire time produce the same key, so a job can
// deduplicate downstream effects (e.g. a payment provider's idempotency header).
func DeriveIdempotencyKey(namespace, name string, fireAt time.Time) string {
	s := "d-cron:v1:" + namespace + ":" + name + ":" + fireAt.UTC().Format(time.RFC3339)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

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
