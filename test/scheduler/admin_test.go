package dcron_test

// Tests for AdminHandler: GET /status, POST /jobs/{name}/pause,
// POST /jobs/{name}/resume, DELETE /jobs/{name} (issue #51, FR-605).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mindfire-test/d-cron/dcron"
)

func newAdminScheduler(t *testing.T) *dcron.Scheduler {
	t.Helper()
	s := testScheduler(newSchedBackend())
	if err := s.Add("alpha", "@every 1m", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Add alpha: %v", err)
	}
	if err := s.Add("beta", "@every 5m", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Add beta: %v", err)
	}
	return s
}

func TestAdminHandler_Status_OK(t *testing.T) {
	s := newAdminScheduler(t)
	h := dcron.AdminHandler(nil, s)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var body struct {
		Jobs []struct {
			Name string `json:"name"`
		} `json:"jobs"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Jobs) != 2 {
		t.Errorf("status: got %d jobs, want 2", len(body.Jobs))
	}
}

func TestAdminHandler_Status_AuthDenied(t *testing.T) {
	s := newAdminScheduler(t)
	h := dcron.AdminHandler(func(*http.Request) bool { return false }, s)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("auth deny: got %d, want 403", rr.Code)
	}
}

func TestAdminHandler_Pause_Resume_Cycle(t *testing.T) {
	s := newAdminScheduler(t)
	h := dcron.AdminHandler(nil, s)

	// Pause
	req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("pause: got %d, want 204", rr.Code)
	}
	// Verify via /status
	jobs := s.Jobs()
	for _, j := range jobs {
		if j.Name == "alpha" && !j.Paused {
			t.Error("pause: job not marked paused")
		}
	}

	// Resume
	req = httptest.NewRequest(http.MethodPost, "/jobs/alpha/resume", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("resume: got %d, want 204", rr.Code)
	}
	for _, j := range s.Jobs() {
		if j.Name == "alpha" && j.Paused {
			t.Error("resume: job still marked paused")
		}
	}
}

func TestAdminHandler_Pause_NotFound(t *testing.T) {
	s := newAdminScheduler(t)
	h := dcron.AdminHandler(nil, s)

	req := httptest.NewRequest(http.MethodPost, "/jobs/ghost/pause", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("pause unknown: got %d, want 404", rr.Code)
	}
}

func TestAdminHandler_Delete_RemovesJob(t *testing.T) {
	s := newAdminScheduler(t)
	h := dcron.AdminHandler(nil, s)

	req := httptest.NewRequest(http.MethodDelete, "/jobs/beta", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", rr.Code)
	}
	for _, j := range s.Jobs() {
		if j.Name == "beta" {
			t.Error("delete: job still present after removal")
		}
	}
}

func TestAdminHandler_Delete_NotFound(t *testing.T) {
	s := newAdminScheduler(t)
	h := dcron.AdminHandler(nil, s)

	req := httptest.NewRequest(http.MethodDelete, "/jobs/ghost", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: got %d, want 404", rr.Code)
	}
}
