package dcron_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/metrics"
)

type recordingRecorder struct {
	leaders     atomic.Int64
	started     atomic.Int64
	finished    atomic.Int64
	lastJob     atomic.Value
	lastOutcome atomic.Value
	lastDone    atomic.Bool
}

func newRecordingRecorder() *recordingRecorder {
	r := &recordingRecorder{}
	r.lastJob.Store("")
	r.lastOutcome.Store("")
	return r
}

func (r *recordingRecorder) SetLeader(_ string, isLeader bool) {
	if isLeader {
		r.leaders.Add(1)
	}
}

func (r *recordingRecorder) LeaderTransition(_ string) {}

func (r *recordingRecorder) JobStarted(_ string) { r.started.Add(1) }

func (r *recordingRecorder) JobFinished(job string, out metrics.Outcome, _ time.Duration, _ bool) {
	r.finished.Add(1)
	r.lastJob.Store(job)
	r.lastOutcome.Store(out.String())
	r.lastDone.Store(true)
}

func (r *recordingRecorder) FencedWrite()       {}
func (r *recordingRecorder) MissedRun(_ string) {}

func TestMetricsRecorderReceivesSignals(t *testing.T) {
	rec := newRecordingRecorder()
	s := testScheduler(newSchedBackend(), dcron.WithMetrics(rec))
	if err := s.Add("job", "@every 1ms", func(context.Context) error { return nil }, dcron.WithRetry(dcron.Retry{Attempts: 1})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return rec.lastDone.Load() }, "job finished")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	if rec.leaders.Load() == 0 {
		t.Fatalf("expected SetLeader(instance,true), saw %d", rec.leaders.Load())
	}
	if rec.started.Load() == 0 || rec.finished.Load() == 0 {
		t.Fatalf("expected JobStarted/JobFinished, got %d/%d", rec.started.Load(), rec.finished.Load())
	}
	if job, _ := rec.lastJob.Load().(string); job != "job" {
		t.Fatalf("metrics JobFinished job = %q; want job", job)
	}
}
