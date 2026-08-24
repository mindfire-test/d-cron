package dcron

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by the scheduler. Callers may compare against them
// with ==.
var (
	// ErrNilDB is returned by New when given a nil database handle.
	ErrNilDB = errors.New("dcron: nil database handle")

	// ErrJobExists is returned when registering a job whose name is already
	// registered.
	ErrJobExists = errors.New("dcron: job already registered")

	// ErrInvalidSpec is returned when a schedule expression cannot be parsed.
	ErrInvalidSpec = errors.New("dcron: invalid schedule specification")

	// ErrNotStarted is returned when an operation requires a started
	// scheduler.
	ErrNotStarted = errors.New("dcron: scheduler not started")

	// ErrAlreadyStarted is returned when an operation requires a scheduler
	// that has not yet been started.
	ErrAlreadyStarted = errors.New("dcron: scheduler already started")

	// ErrNilJob is returned when Add receives a nil job function.
	ErrNilJob = errors.New("dcron: nil job function")

	// ErrNotLeader is returned when an action requires the scheduler to be the
	// elected leader.
	ErrNotLeader = errors.New("dcron: not the leader")

	// ErrSessionStabilityUnasserted is returned by New when the operator has not
	// asserted session stability or supplied a dedicated lock connection (SDS
	// §3.4, issue #12). A transaction-mode pooler silently corrupts
	// advisory-lock semantics — two simultaneous leaders and an orphaned lock —
	// so d-cron refuses to start rather than guess. Remedies: pass
	// WithSessionStableConnection(), supply WithDedicatedLockConn/WithDedicatedLockDSN,
	// configure PgBouncer in session mode, or wait for the Phase 4 direct
	// connection path.
	ErrSessionStabilityUnasserted = errors.New("dcron: session stability not asserted; refusing to start. PgBouncer in transaction mode corrupts advisory-lock semantics (two leaders, orphaned lock). Pass WithSessionStableConnection() to assert a direct/session-mode connection, or WithDedicatedLockConn/WithDedicatedLockDSN to supply a dedicated connection that bypasses any pooler")
)

// Typed, actionable errors (issue #26). Each carries the identity of the
// affected job or configuration key and implements errors.Is against the
// matching sentinel above, so callers can keep using errors.Is while also
// errors.As-ing for the structured type and its context.

// JobExistsError is returned by Add when name is already registered. It names
// the duplicate job so callers can report it directly (issue #26).
type JobExistsError struct{ Name string }

// Error implements error, naming the affected job (issue #26).
func (e *JobExistsError) Error() string {
	return fmt.Sprintf("dcron: job %q already registered", e.Name)
}

// Is allows errors.Is(err, ErrJobExists) to match (issue #26).
func (e *JobExistsError) Is(target error) bool { return target == ErrJobExists }

// InvalidSpecError is returned by Add when a job's schedule cannot be parsed. It
// names the job and the offending spec so the operator fixes the registration
// call, not just reads the error string (issue #26).
type InvalidSpecError struct {
	Name string
	Spec string
}

// Error implements error.
func (e *InvalidSpecError) Error() string {
	return fmt.Sprintf("dcron: job %q has invalid schedule %q", e.Name, e.Spec)
}

// Is allows errors.Is(err, ErrInvalidSpec) to match (issue #26).
func (e *InvalidSpecError) Is(target error) bool { return target == ErrInvalidSpec }

// SessionStabilityError is returned by New when the session-stability gate
// (SDS §3.4, issue #12) was not satisfied. It names the affected configuration
// (the lock connection / session-stability assertion) so operators know which
// remediation to apply (issue #26).
type SessionStabilityError struct{}

// Error implements error.
func (e *SessionStabilityError) Error() string { return ErrSessionStabilityUnasserted.Error() }

// Is allows errors.Is(err, ErrSessionStabilityUnasserted) to match (issue #26).
func (e *SessionStabilityError) Is(target error) bool {
	return target == ErrSessionStabilityUnasserted
}
