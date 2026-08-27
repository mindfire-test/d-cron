package dcron

import (
	"context"
	"fmt"
	"time"

	"github.com/mindfire-test/d-cron/internal/elector"
)

// LeadershipState is a three-valued enum describing this replica's position in
// leader election (SDS §3.5, issue #37/FR-109). A bool would collapse "not
// leader" and "don't know" — exactly the distinction a Kubernetes readiness probe
// needs: a replica that doesn't know whether it's leader must not receive
// traffic for leadership-sensitive endpoints.
type LeadershipState int

const (
	// LeadershipUnknown is the zero value: before the first poll, or after a
	// transient error left the state uncertain.
	LeadershipUnknown LeadershipState = iota
	// LeadershipStandby means this replica is not the leader and is polling
	// for promotion, or was recently demoting.
	LeadershipStandby
	// LeadershipLeader means this replica holds the advisory lock and is
	// running the schedule clock.
	LeadershipLeader
)

// String returns a short, log-friendly label for the state.
func (l LeadershipState) String() string {
	switch l {
	case LeadershipStandby:
		return "standby"
	case LeadershipLeader:
		return "leader"
	default:
		return "unknown"
	}
}

// Leadership reports the current membership state of this replica (issue #37,
// FR-109). Unlike a bool, it distinguishes "not leader" from "don't know".
func (s *Scheduler) Leadership() LeadershipState {
	switch s.leader.State() {
	case elector.StateLeader:
		return LeadershipLeader
	case elector.StateStandby, elector.StateDemoting:
		return LeadershipStandby
	default:
		return LeadershipUnknown
	}
}

// HealthCheck reports whether the coordination backend is reachable. It is
// suitable for use as a Kubernetes liveness/readiness probe (issue #37,
// FR-411). A nil error means the backend responded to a ping.
func (s *Scheduler) HealthCheck(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("dcron: health check failed: %w", err)
	}
	return nil
}

// JobStatus captures the observable state of one registered job for the
// dashboard and metrics (issue #37/FR-406). Fields are a point-in-time
// snapshot; call Jobs() repeatedly for live updates.
type JobStatus struct {
	Name    string
	Spec    string
	NextRun time.Time
	LastRun time.Time
	// LastOutcome is the outcome label of the most recent execution
	// ("ok", "failed", "panicked", "timed_out", "canceled", "unknown").
	LastOutcome  string
	LastError    string
	LastDuration time.Duration
	Running      bool
	Paused       bool
}

// Jobs returns a point-in-time snapshot of every registered job's status
// (issue #37, FR-406). The returned slice is safe to read and mutate.
func (s *Scheduler) Jobs() []JobStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	statuses := make([]JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		j.statusMu.Lock()
		status := JobStatus{
			Name:         j.name,
			Spec:         j.spec,
			NextRun:      j.nextRun,
			LastRun:      j.lastRun,
			LastOutcome:  j.lastOutcome,
			LastError:    j.lastError,
			LastDuration: j.lastDuration,
			Running:      j.running,
			Paused:       j.paused,
		}
		j.statusMu.Unlock()
		statuses = append(statuses, status)
	}
	return statuses
}
