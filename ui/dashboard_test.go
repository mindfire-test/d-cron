package ui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerRendersOverviewAndJobs(t *testing.T) {
	t.Parallel()
	overview := func() Overview {
		return Overview{
			Namespace:      "billing",
			InstanceID:     "deadbeef",
			LockKey:        -7300000000000000000,
			Leadership:     "leader",
			HistoryEnabled: true,
			Schema:         "dcron",
			Jobs: []JobRow{{
				Name:           "send-invoices",
				Spec:           "0 2 * * *",
				NextRun:        time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC),
				LastRun:        time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC),
				LastOutcome:    "ok",
				LastDurationMS: 120,
			}},
		}
	}
	h := Handler(func() Overview { return overview() }, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	for _, want := range []string{"billing", "deadbeef", "leader", "send-invoices", "0 2 * * *", "ok"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q\n%s", want, body)
		}
	}
	if !strings.Contains(body, "no authentication") && !strings.Contains(body, "authentication or authorization") {
		t.Error("dashboard must state that auth is the host app's responsibility (FR-408)")
	}
}

func TestHandlerRendersHistory(t *testing.T) {
	t.Parallel()
	recent := func(_ context.Context) []HistoryRow {
		return []HistoryRow{{
			ScheduledAt: time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC),
			Job:         "send-invoices",
			Status:      "failed",
			Attempt:     3,
			DurationMS:  4500,
			InstanceID:  "cafe",
			Error:       "boom",
		}}
	}
	h := Handler(func() Overview { return Overview{Leadership: "standby"} }, recent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for _, want := range []string{"send-invoices", "failed", "boom", "cafe"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %q\n%s", want, rec.Body.String())
		}
	}
}

func TestHandlerEscapesJobNames(t *testing.T) {
	t.Parallel()
	// A job name is caller data: it MUST be escaped, not interpreted (#38).
	h := Handler(func() Overview {
		return Overview{Leadership: "unknown"}
	}, func(context.Context) []HistoryRow {
		return []HistoryRow{{Job: "<script>alert(1)</script>", Status: "ok"}}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("job name was not HTML-escaped:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("expected escaped script tag in body")
	}
}

func TestHandlerEmptyStates(t *testing.T) {
	t.Parallel()
	h := Handler(func() Overview { return Overview{Leadership: "standby"} }, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "No jobs registered") || !strings.Contains(body, "history is disabled") {
		t.Errorf("empty-state notices missing:\n%s", body)
	}
}
