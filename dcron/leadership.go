package dcron

import (
	"context"
	"fmt"
	"time"

	"github.com/mindfire-test/d-cron/internal/elector"
)

// LeadershipState describes this replica's position in leader election.
type LeadershipState int

const (
	LeadershipUnknown LeadershipState = iota
	LeadershipStandby
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

// Leadership reports the current membership state of this replica.
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

// HealthCheck reports whether the coordination backend is reachable.
func (s *Scheduler) HealthCheck(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("dcron: health check failed: %w", err)
	}
	return nil
}

// JobStatus captures the observable state of one registered job.
type JobStatus struct {
	Name         string
	Spec         string
	NextRun      time.Time
	LastRun      time.Time
	LastOutcome  string
	LastError    string
	LastDuration time.Duration
	Running      bool
	Paused       bool
}

// Jobs returns a point-in-time snapshot of every registered job's status.
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
