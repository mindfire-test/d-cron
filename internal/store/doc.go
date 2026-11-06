// Package store persists leader epochs and run history via idempotent
// migrations.
//
// History tracking is opt-in via dcron.WithHistory. The schema lives in its
// own schema (default `dcron`, never `public`), created with IF NOT EXISTS
// DDL guarded by a separate advisory lock so replicas starting concurrently
// do not race. All queries are parameterised; the only interpolated
// identifier is the schema name, which is validated against an allowlist.
//
// The leader_epoch table and execution table carry leader epoch fencing
// tokens to guard both opening and terminal execution writes against demoted
// leaders.
package store
