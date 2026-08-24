// Command with-dashboard demonstrates the Phase 2 observability integration:
// mounting ui.Handler on an application router and wiring a Prometheus-style
// recorder into dcron.WithMetrics.
//
// The scheduler core never links a metrics SDK (SDS NFR-402): the Recorder
// below is the app-side adapter, and the dashboard is served by the separate
// ui package. Authentication for /internal/dcron is deliberately NOT included
// (FR-408) — protect it in your own middleware.
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	dcron "github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/metrics"
	"github.com/mindfire-test/d-cron/ui"
	// Register your PostgreSQL driver here, e.g. `_ "github.com/lib/pq"`.
)

// promRecorder is an example metrics.Recorder. In production you would bridge
// these calls to a prometheus.Registry via prometheus/client_golang; the
// metric names to register are exported from the metrics package as Key*.
type promRecorder struct {
	metrics.Noop
}

func main() {
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	sched, err := dcron.New(
		db,
		dcron.WithNamespace("with-dashboard"),
		dcron.WithSessionStableConnection(),
		dcron.WithHistory(7*24*time.Hour), // opt-in schema `dcron` (#34)
		dcron.WithHooks(&dcron.WebhookHook{
			URL:     os.Getenv("DCRON_WEBHOOK_URL"),
			Timeout: 5 * time.Second,
		}),
		dcron.WithMetrics(&promRecorder{}),
	)
	if err != nil {
		log.Fatalf("dcron.New: %v", err)
	}
	if err := sched.Add("report", "*/5 * * * *", func(ctx context.Context) error {
		log.Println("report", dcron.Epoch(ctx))
		return nil
	}); err != nil {
		log.Fatalf("Add: %v", err)
	}

	if err := sched.Start(context.Background()); err != nil {
		log.Fatalf("Start: %v", err)
	}

	// Dashboard (issue #38): read-only, server-rendered, embedded assets.
	// Mount wherever you like; PROTECT IT — no auth is performed here.
	mux := http.NewServeMux()
	mux.Handle("/internal/dcron", http.StripPrefix("/internal/dcron",
		ui.Handler(
			func() ui.Overview {
				return ui.Overview{
					Namespace:      sched.Namespace(),
					InstanceID:     sched.InstanceID(),
					LockKey:        sched.Key(),
					Leadership:     sched.Leadership().String(),
					HistoryEnabled: true,
					Schema:         "dcron",
				}
			},
			nil, // wire ui history rows from store.Recent when desired
		)))
	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Println("dashboard on http://localhost:8080/internal/dcron")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("dashboard server: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	drain, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sched.Stop(drain); err != nil {
		log.Printf("stop: %v", err)
	}
	shutdown, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_ = srv.Shutdown(shutdown)
}
