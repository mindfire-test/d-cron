package ui

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"time"
)

// Overview is the point-in-time scheduler state the dashboard renders (issue
// #38, FR-406). The host application fills it from its dcron.Scheduler —
// typically via Leadership, Key, InstanceID, and Jobs — so this package stays
// decoupled from (and optional to) the scheduler core.
type Overview struct {
	Namespace  string
	InstanceID string
	LockKey    int64
	// Leadership is one of "leader", "standby", "unknown".
	Leadership string
	// HistoryEnabled and Schema describe the opt-in history store (#34).
	HistoryEnabled bool
	Schema         string
	// Jobs is the registered-jobs snapshot (dcron.Scheduler.Jobs).
	Jobs []JobRow
}

// JobRow mirrors one entry of dcron.Scheduler.Jobs for rendering.
type JobRow struct {
	Name           string
	Spec           string
	NextRun        time.Time
	LastRun        time.Time
	LastOutcome    string
	LastError      string
	LastDurationMS int64
	Running        bool
}

// HistoryRow mirrors one execution from the history store for rendering.
type HistoryRow struct {
	ScheduledAt time.Time
	Job         string
	Status      string
	Attempt     int
	DurationMS  int64
	InstanceID  string
	Error       string
}

type page struct {
	Overview
	History []HistoryRow
}

// HistoryFunc returns the most recent executions to display. It may return nil
// when history is disabled; the dashboard then renders a disabled notice.
type HistoryFunc func(ctx context.Context) []HistoryRow

// Handler returns a read-only, server-rendered dashboard (issue #38, FR-405).
// Mount it at any path with the standard library mux or any router:
//
//	http.Handle("/internal/dcron/", ui.Handler(overview, recent))
//
// The page self-refreshes via a meta tag: no JavaScript, no CDN, no build step
// (FR-407). It performs NO authentication or authorization — protecting the
// endpoint is entirely the host application's responsibility (FR-408).
//
// overview must be non-nil; recent may be nil. A panic in either callback is
// not recovered here: they run on the caller's own scheduler primitives.
func Handler(overview func() Overview, recent HistoryFunc) http.Handler {
	tmpl := template.Must(template.ParseFS(Assets, "assets/index.html"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if overview == nil {
			http.Error(w, "dcron dashboard: no overview provider configured", http.StatusInternalServerError)
			return
		}
		p := page{Overview: overview()}
		if recent != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			p.History = recent(ctx)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := tmpl.Execute(w, p); err != nil {
			slog.Error("ui: render dashboard", "err", err)
		}
	})
}
