// Package otel provides OpenTelemetry integration adapters for d-cron job executions (issue #47, FR-410).
//
// The package is isolated from core d-cron so applications that do not use OTel
// do not link OpenTelemetry dependencies (NFR-402).
package otel

import (
	"context"
	"time"
)

// Tracer defines the interface for tracing job executions (issue #47, FR-410).
type Tracer interface {
	StartSpan(ctx context.Context, jobName string, scheduledAt time.Time, attempt int, epoch int64) (context.Context, func(outcome string, err error))
}

// NoopTracer is the default zero-overhead tracer implementation.
type NoopTracer struct{}

// StartSpan returns the unchanged context and a no-op end function.
func (n NoopTracer) StartSpan(ctx context.Context, _ string, _ time.Time, _ int, _ int64) (context.Context, func(_ string, _ error)) {
	return ctx, func(_ string, _ error) {}
}
