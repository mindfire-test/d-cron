// Package store persists leader epochs and run history via idempotent
// migrations.
//
// Phase 2 work (opt-in via dcron.WithHistory): the schema lives in its own
// `dcron` schema (never `public`), is created with IF NOT EXISTS DDL wrapped
// in a transaction and guarded by a separate advisory lock so N replicas
// starting at once do not race (SDS §10, issue #34/FR-504). All queries are
// parameterised; the only interpolated identifier is the schema name, which is
// validated against an allowlist (NFR-503).
//
// The leader_epoch table is Phase 3 work (issue #41): the execution table
// carries a leader_epoch column now so Phase 3 fencing can guard both the
// opening and terminal writes without a migration.
package store
