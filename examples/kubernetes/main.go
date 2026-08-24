// Command kubernetes demonstrates deploying d-cron inside a 5-replica
// Kubernetes Deployment (AC-01..AC-03): exactly-once firing under normal
// operation, graceful leader failover, and split-brain containment.
//
// Every replica runs this same binary. Exactly one becomes leader; the other
// four stand by on a jittered poll. /healthz exposes dcron.HealthCheck as a
// liveness probe (coordination backend reachability), and /readyz additionally
// reports leadership state so traffic can be shifted away from replicas that
// do not currently know they are leader.
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
	// Register your PostgreSQL driver here, e.g. `_ "github.com/lib/pq"`.
)

func main() {
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	sched, err := dcron.New(
		db,
		dcron.WithNamespace(os.Getenv("DCRON_NAMESPACE")),
		dcron.WithPollInterval(5*time.Second),
		dcron.WithSessionStableConnection(),
	)
	if err != nil {
		log.Fatalf("dcron.New: %v", err)
	}
	if err := sched.Add("tick", "@every 1m", func(ctx context.Context) error {
		log.Println("tick fired", dcron.IdempotencyKey(ctx))
		return nil
	}); err != nil {
		log.Fatalf("Add: %v", err)
	}
	if err := sched.Start(context.Background()); err != nil {
		log.Fatalf("Start: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Liveness: coordination backend reachable. Failing this gets the pod
		// restarted, which releases the advisory lock promptly.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := sched.HealthCheck(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		// Readiness: healthy AND not unknown. A standby IS ready to serve
		// application traffic; it simply does not run the clock.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := sched.HealthCheck(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if sched.Leadership() == dcron.LeadershipUnknown {
			http.Error(w, "leadership state unknown", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("probe server: %v", err)
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
