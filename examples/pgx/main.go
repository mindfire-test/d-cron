// Command pgx demonstrates running d-cron with github.com/jackc/pgx/v5 as the
// PostgreSQL driver (issues #24/#27, FR-506).
//
// The core library stays database/sql-based and dependency-free; this module
// imports pgx so applications that prefer lib/pq never link it. The stdlib
// adapter registers the driver under the name "pgx", which is also passed to
// WithDedicatedLockDriver so the advisory lock gets its own direct connection
// (bypassing any PgBouncer in front of the app pool).
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers driver name "pgx"

	dcron "github.com/mindfire-test/d-cron/dcron"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required (postgres://... form)")
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open via pgx stdlib: %v", err)
	}
	defer sqlDB.Close()

	sched, err := dcron.New(sqlDB,
		dcron.WithNamespace("pgx-example"),
		// The advisory lock must live on a session-stable connection. Give
		// d-cron its own direct connection by DSN + driver name; the pool
		// above can then sit behind any pooler.
		dcron.WithDedicatedLockDriver("pgx", dsn),
	)
	if err != nil {
		log.Fatalf("dcron.New: %v", err)
	}
	if err := sched.Add("heartbeat", "@every 10s", func(ctx context.Context) error {
		log.Println("heartbeat on replica", sched.InstanceID(), "epoch", dcron.Epoch(ctx))
		return nil
	}); err != nil {
		log.Fatalf("Add: %v", err)
	}
	if err := sched.Start(context.Background()); err != nil {
		log.Fatalf("Start: %v", err)
	}
	log.Printf("running as %s (lock key %d)", sched.Namespace(), sched.Key())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	drain, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sched.Stop(drain); err != nil {
		log.Printf("stop: %v", err)
	}
}