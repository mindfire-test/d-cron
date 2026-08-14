// Package elector implements PostgreSQL-advisory-lock based leader election,
// the leadership state machine, and pooler/session-stability detection.
//
// This package is Phase 1 work and currently contains no implementation. See
// the SDS §3 for the lock lifecycle, split-brain fencing, and C-07 regression
// requirements it must satisfy.
package elector
