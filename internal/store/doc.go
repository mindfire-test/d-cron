// Package store persists leader epochs and run history via idempotent
// migrations.
//
// This package is Phase 2 work (opt-in schema, never public and never created
// during Phase 1) and currently contains no implementation. See the SDS §10
// for the schema and migration requirements.
package store
