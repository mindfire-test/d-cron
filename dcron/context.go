package dcron

import "context"

// Context keys for values injected into job execution contexts. The private,
// unexported key types prevent collisions with values set by application code.
type (
	epochKey       struct{}
	idempotencyKey struct{}
)

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
