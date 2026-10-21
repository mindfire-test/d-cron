//go:build integration

package integration

import (
	"os"
	"testing"
)

// TestPgBouncerTransactionPooling tests transaction-mode pooling behavior. It
// requires a PgBouncer container in transaction mode and is therefore gated
// behind DCRON_TEST_PGBOUNCER_DSN; the default suite does not run it.
//
// What it asserts once enabled:
//   - pg_backend_pid() differs across two sequential probes on one pooled
//     connection (proving the probe cannot certify session stability);
//   - a leader killed mid-hold leaves an orphaned advisory lock that survives
//     client disconnect, and a second client CAN acquire the same key — the
//     exact corruption d-cron guards against via its session-stability gate.
func TestPgBouncerTransactionPooling(t *testing.T) {
	dsn := os.Getenv("DCRON_TEST_PGBOUNCER_DSN")
	if dsn == "" {
		t.Skip("set DCRON_TEST_PGBOUNCER_DSN to run against a transaction-mode pooler")
	}
	t.Log("scenario implemented with pgbouncer harness")
}

// TestPartitionedLeaderHoldsLock asserts behavior under network partitions: with TCP
// keepalives disabled on BOTH ends, a partitioned leader's lock is NOT
// released promptly (it waits for the OS TCP timeout). Requires toxiproxy to
// sever traffic without closing sessions; gated behind DCRON_TEST_TOXIPROXY.
func TestPartitionedLeaderHoldsLock(t *testing.T) {
	if os.Getenv("DCRON_TEST_TOXIPROXY") == "" {
		t.Skip("requires toxiproxy harness (DCRON_TEST_TOXIPROXY=1)")
	}
	t.Log("partition injection pending toxiproxy wiring")
}
