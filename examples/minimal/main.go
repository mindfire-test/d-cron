// Command minimal demonstrates the most compact d-cron integration.
//
// Run against any reachable PostgreSQL 12+ instance:
//
//	postgres://user:pass@localhost:5432/db?sslmode=disable
//
// The session-stability assertion (dcron.WithSessionStableConnection) is the
// one mandatory option (issue #12): d-cron refuses to start without it or a
// dedicated lock connection, because a transaction-mode pooler silently breaks
// advisory-lock semantics.
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	dcron "github.com/mindfire-test/d-cron/dcron"
)

func main() {
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	sched, err := dcron.New(
		db,
		dcron.WithNamespace("minimal"),
		dcron.WithSessionStableConnection(),
	)
	if err != nil {
		log.Fatalf("dcron.New: %v", err)
	}

	if err := sched.Add("heartbeat", "@every 10s", func(ctx context.Context) error {
		log.Println("heartbeat", dcron.IdempotencyKey(ctx))
		return nil
	}); err != nil {
		log.Fatalf("Add: %v", err)
	}
	if err := sched.AddOnce("one-off", time.Now().Add(30*time.Second), func(ctx context.Context) error {
		log.Println("one-off fired once")
		return nil
	}); err != nil {
		log.Fatalf("AddOnce: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := sched.Start(ctx); err != nil {
		log.Fatalf("Start: %v", err)
	}
	log.Printf("d-cron running (lock key %d); ctrl-C to stop", sched.Key())

	<-ctx.Done()

	drain, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sched.Stop(drain); err != nil {
		log.Printf("stop: %v", err)
	}
}
