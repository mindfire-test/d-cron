package dcron

import (
	"errors"
	"fmt"
)

/* Sentinel errors returned by the scheduler. */
var (
	/* ErrNilDB is returned by New when given a nil database handle. */
	ErrNilDB = errors.New("dcron: nil database handle")

	/* ErrJobExists is returned when registering a job whose name is already registered. */
	ErrJobExists = errors.New("dcron: job already registered")

	/* ErrInvalidSpec is returned when a schedule expression cannot be parsed. */
	ErrInvalidSpec = errors.New("dcron: invalid schedule specification")

	/* ErrNotStarted is returned when an operation requires a started scheduler. */
	ErrNotStarted = errors.New("dcron: scheduler not started")

	/* ErrAlreadyStarted is returned when an operation requires a scheduler that has not yet been started. */
	ErrAlreadyStarted = errors.New("dcron: scheduler already started")

	/* ErrNilJob is returned when Add receives a nil job function. */
	ErrNilJob = errors.New("dcron: nil job function")

	/* ErrNotLeader is returned when an action requires the scheduler to be the elected leader. */
	ErrNotLeader = errors.New("dcron: not the leader")

	/* ErrJobNotFound is returned when the given job name is not registered. */
	ErrJobNotFound = errors.New("dcron: job not found")

	/* ErrSessionStabilityUnasserted is returned by New when session stability was not asserted. */
	ErrSessionStabilityUnasserted = errors.New("dcron: session stability not asserted; refusing to start. PgBouncer in transaction mode corrupts advisory-lock semantics (two leaders, orphaned lock). Pass WithSessionStableConnection() to assert a direct/session-mode connection, or WithDedicatedLockConn/WithDedicatedLockDSN to supply a dedicated connection that bypasses any pooler")
)

/* JobExistsError is returned by Add when name is already registered. */
type JobExistsError struct{ Name string }

/* Error implements error. */
func (e *JobExistsError) Error() string {
	return fmt.Sprintf("dcron: job %q already registered", e.Name)
}

/* Is allows errors.Is(err, ErrJobExists) to match. */
func (e *JobExistsError) Is(target error) bool { return target == ErrJobExists }

/* InvalidSpecError is returned by Add when a job's schedule cannot be parsed. */
type InvalidSpecError struct {
	Name string
	Spec string
}

/* Error implements error. */
func (e *InvalidSpecError) Error() string {
	return fmt.Sprintf("dcron: job %q has invalid schedule %q", e.Name, e.Spec)
}

/* Is allows errors.Is(err, ErrInvalidSpec) to match. */
func (e *InvalidSpecError) Is(target error) bool { return target == ErrInvalidSpec }

/* SessionStabilityError is returned by New when session stability is not satisfied. */
type SessionStabilityError struct{}

/* Error implements error. */
func (e *SessionStabilityError) Error() string { return ErrSessionStabilityUnasserted.Error() }

/* Is allows errors.Is(err, ErrSessionStabilityUnasserted) to match. */
func (e *SessionStabilityError) Is(target error) bool {
	return target == ErrSessionStabilityUnasserted
}

/* JobNotFoundError is returned when the given job name is not registered. */
type JobNotFoundError struct{ Name string }

/* Error implements error. */
func (e *JobNotFoundError) Error() string {
	return fmt.Sprintf("dcron: job %q not found", e.Name)
}

/* Is allows errors.Is(err, ErrJobNotFound) to match. */
func (e *JobNotFoundError) Is(target error) bool { return target == ErrJobNotFound }
