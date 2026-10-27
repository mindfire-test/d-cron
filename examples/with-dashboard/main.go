// Command with-dashboard demonstrates observability integration:
// mounting ui.Handler on an application router and wiring a Prometheus-style
// recorder into dcron.WithMetrics.
//
// The scheduler core avoids external metrics dependencies: the Recorder
// below is the application-side adapter, and the dashboard is served by the separate
// ui package. Authentication for /internal/dcron is intentionally left to the host
// application — protect it using your own middleware.
package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	dcron "github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/metrics"
	"github.com/mindfire-test/d-cron/ui"
)

type promRecorder struct {
	metrics.Noop
}

func recentHistory(ctx context.Context, db *sql.DB, namespace string, limit int) []ui.HistoryRow {
	const query = `SELECT scheduled_at, job_name, status, attempt, duration_ms, instance_id, error
		FROM dcron.execution
		WHERE namespace = $1
		ORDER BY scheduled_at DESC
		LIMIT $2`
	rows, err := db.QueryContext(ctx, query, namespace, limit)
	if err != nil {
		log.Printf("dashboard history: %v", err)
		return nil
	}
	defer rows.Close()

	rowsOut := make([]ui.HistoryRow, 0, limit)
	for rows.Next() {
		var row ui.HistoryRow
		var dur sql.NullInt64
		var errText sql.NullString
		if err := rows.Scan(
			&row.ScheduledAt,
			&row.Job,
			&row.Status,
			&row.Attempt,
			&dur,
			&row.InstanceID,
			&errText,
		); err != nil {
			log.Printf("dashboard history scan: %v", err)
			return nil
		}
		if dur.Valid {
			row.DurationMS = dur.Int64
		}
		if errText.Valid {
			row.Error = errText.String
		}
		rowsOut = append(rowsOut, row)
	}
	if err := rows.Err(); err != nil {
		log.Printf("dashboard history rows: %v", err)
		return nil
	}
	return rowsOut
}

func jobRows(s *dcron.Scheduler) []ui.JobRow {
	statuses := s.Jobs()
	rows := make([]ui.JobRow, 0, len(statuses))
	for _, st := range statuses {
		rows = append(rows, ui.JobRow{
			Name:           st.Name,
			Spec:           st.Spec,
			NextRun:        st.NextRun,
			LastRun:        st.LastRun,
			LastOutcome:    st.LastOutcome,
			LastError:      st.LastError,
			LastDurationMS: st.LastDuration.Milliseconds(),
			Running:        st.Running,
		})
	}
	return rows
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
		dcron.WithHistory(7*24*time.Hour),
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

	// Security: Require a bearer token for administrative and dashboard endpoints by default.
	// In production, configure DCRON_AUTH_TOKEN in the environment.
	authToken := os.Getenv("DCRON_AUTH_TOKEN")
	if authToken == "" {
		authToken = "dev-secret-token" // Safe default for local testing
		log.Println("WARNING: DCRON_AUTH_TOKEN not set; defaulting to 'dev-secret-token' for local demo")
	}

	dashboardHandler := ui.Handler(
		func() ui.Overview {
			return ui.Overview{
				Namespace:      sched.Namespace(),
				InstanceID:     sched.InstanceID(),
				LockKey:        sched.Key(),
				Leadership:     sched.Leadership().String(),
				HistoryEnabled: true,
				Schema:         "dcron",
				Jobs:           jobRows(sched),
			}
		},
		func(ctx context.Context) []ui.HistoryRow {
			return recentHistory(ctx, db, sched.Namespace(), 20)
		},
	)

	mux := http.NewServeMux()
	mux.Handle("/internal/dcron", requireBearerToken(authToken, http.StripPrefix("/internal/dcron", dashboardHandler)))
	mux.Handle("/internal/dcron/api/", requireBearerToken(authToken, http.StripPrefix("/internal/dcron/api", dcron.AdminHandler(dcron.RequireBearerToken(authToken), sched))))

	// Bind to localhost only by default so unauthenticated internal management UI
	// is not exposed across external or container network interfaces.
	srv := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Println("dashboard on http://127.0.0.1:8080/internal/dcron")
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

// requireBearerToken protects HTTP handlers by verifying an Authorization: Bearer <token> header.
func requireBearerToken(expectedToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="dcron-dashboard"`)
			http.Error(w, "unauthorized: bearer token required", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			http.Error(w, "forbidden: invalid token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
