package dcron

import "errors"

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

	// ErrNotLeader is returned when an action requires the scheduler to be the
	// elected leader.
	ErrNotLeader = errors.New("dcron: not the leader")
)
