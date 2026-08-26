package otel_test

import (
	"context"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/otel"
)

func TestNoopTracer(t *testing.T) {
	t.Parallel()

	tracer := otel.NoopTracer{}
	ctx, endSpan := tracer.StartSpan(context.Background(), "test-job", time.Now(), 1, 42)
	if ctx == nil {
		t.Fatal("StartSpan returned nil context")
	}
	endSpan("success", nil)
}
