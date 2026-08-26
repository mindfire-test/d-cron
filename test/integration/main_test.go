//go:build integration

// Package integration runs d-cron against real PostgreSQL via testcontainers
// (issue #28, NFR-204). It is a SEPARATE Go module so the heavyweight
// testcontainers dependency never lands in the root go.mod that library
// consumers download.
//
// Run:
//
//	go test -tags integration ./...        # uses Docker; starts PG 16
//	DCRON_TEST_DSN=postgres://... go test -tags integration ./...
//
// Without Docker and without DCRON_TEST_DSN every test skips itself — CI on
// machines without a daemon stays green.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

var (
	testDSN string
	testDB  *sql.DB
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dsn := os.Getenv("DCRON_TEST_DSN")
	if dsn == "" {
		var closer func()
		var err error
		dsn, closer, err = startPostgresContainer(ctx)
		if err != nil {
			log.Printf("SKIP suite: no DCRON_TEST_DSN and container start failed (%v)", err)
			os.Exit(0)
		}
		defer closer()
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open %s: %v", redact(dsn), err)
	}
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping %s: %v", redact(dsn), err)
	}
	testDSN, testDB = dsn, db

	code := m.Run()
	_ = db.Close()
	os.Exit(code)
}

func redact(dsn string) string {
	i, j := strings.Index(dsn, "://"), strings.LastIndex(dsn, "@")
	if i >= 0 && j > i {
		return fmt.Sprintf("%s***%s", dsn[:i+3], dsn[j:])
	}
	return dsn
}

func mustOpen(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
