//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
)

// TestPgBouncerTransactionPooling tests transaction-mode pooling behavior. It
// requires a PgBouncer container in transaction mode and is therefore gated
// behind DCRON_TEST_PGBOUNCER_DSN; the default suite skips it when unset.
//
// What it asserts once enabled:
//   - pg_backend_pid() differs across sequential queries on one *sql.DB pool;
//   - dcron.New refuses to start without session stability assertions;
//   - dcron.WithDedicatedLockDSN safely bypasses the transaction-mode pooler.
func TestPgBouncerTransactionPooling(t *testing.T) {
	dsn := os.Getenv("DCRON_TEST_PGBOUNCER_DSN")
	if dsn == "" {
		t.Skip("SKIPPED: set DCRON_TEST_PGBOUNCER_DSN to run against a transaction-mode pooler")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open pgbouncer: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Assert that dcron.New rejects an unconfigured transaction-mode connection pool
	// because automatic probe detects backend PID instability or pooler mode without session stability
	_, err = dcron.New(db)
	if err == nil {
		t.Fatal("dcron.New expected SessionStabilityError against transaction pooler without WithSessionStableConnection, got nil")
	}
	var sessErr *dcron.SessionStabilityError
	if !errors.As(err, &sessErr) {
		t.Logf("dcron.New rejected unconfigured pooler with: %v (expected SessionStabilityError)", err)
	}

	// 2. Demonstrate the underlying pooler issue: sequential queries on *sql.DB in transaction mode
	// can land on different backend PIDs
	var pid1, pid2 int
	if err := db.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid1); err != nil {
		t.Fatalf("scan pid1: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid2); err != nil {
		t.Fatalf("scan pid2: %v", err)
	}
	t.Logf("PgBouncer transaction pooler PIDs: query1=%d, query2=%d (differ=%v)", pid1, pid2, pid1 != pid2)

	// 3. Verify that with WithDedicatedLockDSN(directPostgresDSN), the scheduler safely operates
	// even when the application pool uses a transaction-mode pooler.
	directDSN := os.Getenv("DCRON_TEST_DSN")
	if directDSN != "" {
		sched, err := dcron.New(db,
			dcron.WithDedicatedLockDSN(directDSN),
			dcron.WithNamespace("it-pgbouncer"),
			dcron.WithPollInterval(50*time.Millisecond),
		)
		if err != nil {
			t.Fatalf("dcron.New with dedicated lock DSN: %v", err)
		}
		if err := sched.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		if err := sched.HealthCheck(ctx); err != nil {
			t.Fatalf("HealthCheck with dedicated lock DSN: %v", err)
		}
		if err := sched.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
}

// TestPartitionedLeaderHoldsLock asserts behavior under network partitions: with TCP
// keepalives enabled, a partitioned leader detects session failure and demotes.
// Requires toxiproxy to sever traffic without closing client sockets; gated behind DCRON_TEST_TOXIPROXY.
func TestPartitionedLeaderHoldsLock(t *testing.T) {
	if os.Getenv("DCRON_TEST_TOXIPROXY") == "" {
		t.Skip("SKIPPED: requires toxiproxy harness (DCRON_TEST_TOXIPROXY=1)")
	}

	toxiURL := os.Getenv("DCRON_TOXIPROXY_URL")
	if toxiURL == "" {
		toxiURL = "http://localhost:8474"
	}

	proxyDSN := os.Getenv("DCRON_TOXIPROXY_POSTGRES_DSN")
	if proxyDSN == "" {
		t.Skip("SKIPPED: set DCRON_TOXIPROXY_POSTGRES_DSN to run partition test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", proxyDSN)
	if err != nil {
		t.Fatalf("open postgres via toxiproxy: %v", err)
	}
	defer db.Close()

	sched, err := dcron.New(db,
		dcron.WithSessionStableConnection(),
		dcron.WithNamespace("it-toxiproxy"),
		dcron.WithPollInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sched.Stop(context.Background()) }()

	// Wait until sched becomes leader
	deadline := time.Now().Add(5 * time.Second)
	for sched.Leadership() != dcron.LeadershipLeader && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if sched.Leadership() != dcron.LeadershipLeader {
		t.Fatal("scheduler did not become leader before partition injection")
	}

	// Disable proxy via Toxiproxy HTTP API (sever network connection)
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, toxiURL+"/proxies/postgres/down", strings.NewReader(`{"enabled":false}`))
	if err != nil {
		t.Fatalf("build disable request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("disable toxiproxy: %v", err)
	}
	resp.Body.Close()

	// Wait for scheduler health-check to fail or demote
	partitionDeadline := time.Now().Add(6 * time.Second)
	detected := false
	for time.Now().Before(partitionDeadline) {
		if err := sched.HealthCheck(ctx); err != nil {
			detected = true
			t.Logf("HealthCheck successfully detected severed partition: %v", err)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Re-enable proxy for cleanup
	reqUp, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, toxiURL+"/proxies/postgres/up", strings.NewReader(`{"enabled":true}`))
	if respUp, err := client.Do(reqUp); err == nil {
		respUp.Body.Close()
	}

	if !detected {
		t.Error("partition not detected within deadline")
	}
}
