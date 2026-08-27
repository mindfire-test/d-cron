//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer wraps an active testcontainers PostgreSQL instance.
type PostgresContainer struct {
	DSN       string
	container testcontainers.Container
}

// NewPostgres launches a clean, isolated PostgreSQL container automatically via
// testcontainers-go (issue #4). The PostgreSQL image version is selectable via
// POSTGRES_TEST_VERSION or DCRON_PG_VERSION env vars (defaulting to postgres:16-alpine).
// If Docker is unavailable locally or container startup fails, the test skips
// cleanly with an informative log message (NFR-204).
func NewPostgres(t testing.TB) (*sql.DB, *PostgresContainer) {
	t.Helper()
	ctx := context.Background()

	dsn := os.Getenv("DCRON_TEST_DSN")
	if dsn != "" {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatalf("testutil: open DCRON_TEST_DSN: %v", err)
		}
		if err := db.PingContext(ctx); err != nil {
			t.Fatalf("testutil: ping DCRON_TEST_DSN: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db, &PostgresContainer{DSN: dsn}
	}

	pgVersion := os.Getenv("POSTGRES_TEST_VERSION")
	if pgVersion == "" {
		pgVersion = os.Getenv("DCRON_PG_VERSION")
	}
	if pgVersion == "" {
		pgVersion = "16-alpine"
	}
	if !strings.Contains(pgVersion, ":") && !strings.HasSuffix(pgVersion, "-alpine") {
		pgVersion = fmt.Sprintf("%s-alpine", pgVersion)
	}
	if !strings.Contains(pgVersion, "/") && !strings.HasPrefix(pgVersion, "postgres:") {
		pgVersion = fmt.Sprintf("postgres:%s", pgVersion)
	}

	req := testcontainers.ContainerRequest{
		Image:        pgVersion,
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_PASSWORD": "dcron-testutil",
			"POSTGRES_USER":     "dcron",
			"POSTGRES_DB":       "dcron_testutil",
		},
		AutoRemove: true,
		Cmd: []string{
			"-c", "tcp_keepalives_idle=30",
			"-c", "tcp_keepalives_interval=10",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").
			WithStartupTimeout(90 * time.Second),
	}

	c, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Skipf("SKIP: Docker unavailable or Postgres container failed to start: %v", err)
		return nil, nil
	}

	t.Cleanup(func() {
		_ = c.Terminate(context.Background())
	})

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("testutil: get host: %v", err)
	}

	port, err := c.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("testutil: get mapped port: %v", err)
	}

	connStr := fmt.Sprintf("postgres://dcron:dcron-testutil@%s:%s/dcron_testutil?sslmode=disable", host, port.Port())

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("testutil: open db: %v", err)
	}

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("testutil: ping db: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db, &PostgresContainer{
		DSN:       connStr,
		container: c,
	}
}

// NewConnection opens a second independent connection to the same PostgreSQL
// database instance for concurrent leader election testing.
func (p *PostgresContainer) NewConnection(t testing.TB) *sql.DB {
	t.Helper()
	if p == nil || p.DSN == "" {
		t.Fatal("testutil: invalid PostgresContainer handle")
	}
	db, err := sql.Open("postgres", p.DSN)
	if err != nil {
		t.Fatalf("testutil: open second connection: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("testutil: ping second connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TerminateBackend Session forcibly terminates backend PostgreSQL sessions
// executing pg_terminate_backend, simulating an abrupt connection drop or host kill.
func (p *PostgresContainer) TerminateBackend(t testing.TB, targetDB *sql.DB) int {
	t.Helper()
	ctx := context.Background()

	controlDB := targetDB
	if controlDB == nil {
		var err error
		controlDB, err = sql.Open("postgres", p.DSN)
		if err != nil {
			t.Fatalf("testutil: open control db for terminate: %v", err)
		}
		defer controlDB.Close()
	}

	var pid int
	err := targetDB.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid)
	if err != nil {
		t.Fatalf("testutil: get target pid: %v", err)
	}

	var terminated bool
	err = controlDB.QueryRowContext(ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&terminated)
	if err != nil {
		t.Fatalf("testutil: terminate backend pid %d: %v", pid, err)
	}

	if terminated {
		return pid
	}
	return 0
}
