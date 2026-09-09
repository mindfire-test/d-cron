package dcron_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/mindfire-test/d-cron/dcron"
)

func TestFence_ZeroEpochBypass(t *testing.T) {
	t.Parallel()
	ctx := context.Background() // Epoch(ctx) == 0
	err := dcron.Fence(ctx, nil)
	if err != nil {
		t.Fatalf("Fence with epoch 0: got %v; want nil", err)
	}
}

func TestFence_NamespaceDefault(t *testing.T) {
	t.Parallel()
	ctx := dcron.WithEpoch(context.Background(), 5)
	if ns := dcron.NamespaceKey(ctx); ns != "default" {
		t.Errorf("NamespaceKey default = %q; want default", ns)
	}
	ctxNs := dcron.WithNamespaceKey(ctx, "custom-ns")
	if ns := dcron.NamespaceKey(ctxNs); ns != "custom-ns" {
		t.Errorf("NamespaceKey = %q; want custom-ns", ns)
	}
}

func TestFence_ContextValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if ep := dcron.Epoch(ctx); ep != 0 {
		t.Errorf("Epoch = %d; want 0", ep)
	}
	if key := dcron.IdempotencyKey(ctx); key != "" {
		t.Errorf("IdempotencyKey = %q; want empty", key)
	}

	ctx = dcron.WithEpoch(ctx, 42)
	ctx = dcron.WithIdempotencyKey(ctx, "idem-123")

	if ep := dcron.Epoch(ctx); ep != 42 {
		t.Errorf("Epoch = %d; want 42", ep)
	}
	if key := dcron.IdempotencyKey(ctx); key != "idem-123" {
		t.Errorf("IdempotencyKey = %q; want idem-123", key)
	}
}

func TestFence_SchemaFallback(t *testing.T) {
	t.Parallel()
	// Test that zero epoch returns immediately even with empty/custom schema
	ctx := context.Background()
	if err := dcron.FenceSchema(ctx, nil, ""); err != nil {
		t.Errorf("FenceSchema epoch 0: %v", err)
	}
	if err := dcron.FenceSchema(ctx, nil, "my_schema"); err != nil {
		t.Errorf("FenceSchema epoch 0 custom schema: %v", err)
	}
}

type mockRowQuerier struct {
	query string
	args  []any
}

func (m *mockRowQuerier) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	m.query = query
	m.args = args
	return nil
}

func TestFenceSchemaQueryBuilding(t *testing.T) {
	t.Parallel()
	ctx := dcron.WithEpoch(context.Background(), 10)
	ctx = dcron.WithNamespaceKey(ctx, "app-ns")

	mq := &mockRowQuerier{}
	// Calling FenceSchema will construct query and pass to QueryRowContext
	// QueryRowContext returns nil sql.Row which panics on Scan, so we catch or verify query
	defer func() {
		_ = recover()
		if !strings.Contains(mq.query, "my_custom_schema.leader_epoch") {
			t.Errorf("query = %q; want my_custom_schema.leader_epoch", mq.query)
		}
		if len(mq.args) != 1 || mq.args[0] != "app-ns" {
			t.Errorf("args = %v; want [app-ns]", mq.args)
		}
	}()

	_ = dcron.FenceSchema(ctx, mq, "my_custom_schema")
}

func TestFenceSchemaDefaultSchemaQueryBuilding(t *testing.T) {
	t.Parallel()
	ctx := dcron.WithEpoch(context.Background(), 10)
	ctx = dcron.WithNamespaceKey(ctx, "app-ns")

	mq := &mockRowQuerier{}
	defer func() {
		_ = recover()
		if !strings.Contains(mq.query, "dcron.leader_epoch") {
			t.Errorf("query = %q; want dcron.leader_epoch", mq.query)
		}
	}()

	_ = dcron.Fence(ctx, mq)
}
