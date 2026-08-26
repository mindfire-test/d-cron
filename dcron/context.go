package dcron

import (
	"context"
	"database/sql"
	"fmt"
)

type (
	namespaceKey   struct{}
	epochKey       struct{}
	idempotencyKey struct{}
)

// WithNamespaceKey returns a copy of ctx carrying the namespace name.
func WithNamespaceKey(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, namespaceKey{}, namespace)
}

// NamespaceKey returns the namespace carried by ctx, or "default" if none was set.
func NamespaceKey(ctx context.Context) string {
	if v, ok := ctx.Value(namespaceKey{}).(string); ok && v != "" {
		return v
	}
	return "default"
}

// WithEpoch returns a copy of ctx carrying the given leader epoch (fencing
// token).
func WithEpoch(ctx context.Context, epoch int64) context.Context {
	return context.WithValue(ctx, epochKey{}, epoch)
}

// Epoch returns the leader epoch (fencing token) carried by ctx, or 0 if none
// was set.
func Epoch(ctx context.Context) int64 {
	v, _ := ctx.Value(epochKey{}).(int64)

	return v
}

// WithIdempotencyKey returns a copy of ctx carrying the given idempotency key.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKey{}, key)
}

// IdempotencyKey returns the idempotency key carried by ctx, or "" if none
// was set.
func IdempotencyKey(ctx context.Context) string {
	v, _ := ctx.Value(idempotencyKey{}).(string)

	return v
}

// Querier is an interface satisfied by *sql.DB, *sql.Conn, and *sql.Tx (NFR-403).
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Fence guards application code writes inside user transactions against stale
// leader executions (issue #43, FR-311). It executes:
//
//	SELECT epoch FROM dcron.leader_epoch WHERE namespace = $1 FOR SHARE
//
// using FOR SHARE lock mode to prevent TOCTOU race conditions. It returns an
// error if the current database leader epoch does not match Epoch(ctx).
func Fence(ctx context.Context, tx Querier) error {
	return FenceSchema(ctx, tx, "dcron")
}

// FenceSchema behaves as Fence but allows specifying a custom schema name.
func FenceSchema(ctx context.Context, tx Querier, schema string) error {
	epoch := Epoch(ctx)
	if epoch == 0 {
		return nil
	}
	namespace := NamespaceKey(ctx)
	if schema == "" {
		schema = "dcron"
	}
	q := `SELECT epoch FROM ` + schema + `.leader_epoch WHERE namespace = $1 FOR SHARE`
	var dbEpoch int64
	if err := tx.QueryRowContext(ctx, q, namespace).Scan(&dbEpoch); err != nil {
		return fmt.Errorf("dcron: fence check failed: %w", err)
	}
	if dbEpoch != epoch {
		return fmt.Errorf("dcron: fenced write rejected: ctx epoch %d != current db epoch %d", epoch, dbEpoch)
	}
	return nil
}
