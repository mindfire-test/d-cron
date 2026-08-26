// Command prometheus-adapter demonstrates the app-side bridge from d-cron's
// dependency-free metrics.Recorder seam to a real Prometheus registry
// (SDS §11, issue #36 / FR-404).
//
// The scheduler core never links client_golang (NFR-402): this module does,
// so applications that do not use Prometheus pay nothing. Metric names are
// taken from the exported metrics.Key* constants, keeping the contract stable
// across releases; labels are `job` and (for the leader gauge) `instance`.
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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	dcron "github.com/mindfire-test/d-cron/dcron"
	"github.com/mindfire-test/d-cron/metrics"
)

// promRecorder implements metrics.Recorder on top of a prometheus.Registerer.
type promRecorder struct {
	instance string

	leader     *prometheus.GaugeVec
	trans      *prometheus.CounterVec
	started    prometheus.Counter // executions started; running gauge derives below
	executions *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	lastOK     *prometheus.GaugeVec
	running    *prometheus.GaugeVec
	fenced     prometheus.Counter
	missed     *prometheus.CounterVec
}

// NewPromRecorder registers every documented metric on reg. Call once at boot.
func NewPromRecorder(reg prometheus.Registerer, instance string) metrics.Recorder {
	r := &promRecorder{
		instance: instance,
		leader: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.KeyIsLeader,
			Help: "1 when this replica holds the leadership lock.",
		}, []string{"instance"}),
		trans: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metrics.KeyLeaderTransitions,
			Help: "Leadership membership transitions observed by this replica.",
		}, []string{"instance"}),
		executions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metrics.KeyJobExecutions,
			Help: "Terminal job executions by outcome.",
		}, []string{"job", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metrics.KeyJobDuration,
			Help:    "Job wall-clock duration.",
			Buckets: prometheus.ExponentialBuckets(0.001, 4, 12), // 1ms .. ~4.2s+
		}, []string{"job"}),
		lastOK: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.KeyJobLastSuccess,
			Help: "Unix time of the last successful run per job.",
		}, []string{"job"}),
		running: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.KeyJobsRunning,
			Help: "Jobs currently executing.",
		}, []string{"job"}),
		fenced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: metrics.KeyFencedWrites,
			Help: "History writes rejected by epoch fencing (split-brain signal).",
		}),
		missed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metrics.KeyMissedRuns,
			Help: "Fire times skipped because no replica could run them in time.",
		}, []string{"job"}),
	}
	reg.MustRegister(r.leader, r.trans, r.executions, r.duration,
		r.lastOK, r.running, r.fenced, r.missed)
	return r
}

func (r *promRecorder) SetLeader(_ string, isLeader bool) {
	v := 0.0
	if isLeader {
		v = 1
	}
	r.leader.WithLabelValues(r.instance).Set(v)
}

func (r *promRecorder) LeaderTransition(_ string) {
	r.trans.WithLabelValues(r.instance).Inc()
}

func (r *promRecorder) JobStarted(job string) {
	r.running.WithLabelValues(job).Inc()
}

func (r *promRecorder) JobFinished(job string, outcome metrics.Outcome, d time.Duration, success bool) {
	r.running.WithLabelValues(job).Dec()
	r.executions.WithLabelValues(job, outcome.String()).Inc()
	r.duration.WithLabelValues(job).Observe(d.Seconds())
	if success {
		r.lastOK.WithLabelValues(job).SetToCurrentTime()
	}
}

func (r *promRecorder) FencedWrite() { r.fenced.Inc() }

func (r *promRecorder) MissedRun(job string) { r.missed.WithLabelValues(job).Inc() }

func main() {
	reg := prometheus.NewRegistry() // app-owned registry, not the default one
	rec := NewPromRecorder(reg, os.Getenv("HOSTNAME"))
	reg.MustRegister(collectors.NewGoCollector())

	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	sched, err := dcron.New(db,
		dcron.WithNamespace("with-prometheus"),
		dcron.WithSessionStableConnection(),
		dcron.WithMetrics(rec),
	)
	if err != nil {
		log.Fatalf("dcron.New: %v", err)
	}
	if err := sched.Add("tick", "@every 5s", func(context.Context) error { return nil }); err != nil {
		log.Fatalf("Add: %v", err)
	}
	if err := sched.Start(context.Background()); err != nil {
		log.Fatalf("Start: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srv := &http.Server{Addr: ":9090", Handler: mux}
	go func() {
		log.Println("metrics on http://localhost:9090/metrics")
		_ = srv.ListenAndServe()
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