package dcron_test

// Tests for AdminHandler: GET /status, POST /jobs/{name}/pause,
// POST /jobs/{name}/resume, DELETE /jobs/{name}.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAdminHandler_Resume_NotFound(t *testing.T) {
	s := newAdminScheduler(t)
	h := dcron.AdminHandler(nil, s)

	req := httptest.NewRequest(http.MethodPost, "/jobs/ghost/resume", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("resume unknown: got %d, want 404", rr.Code)
	}
}

func TestAdminHandler_AuthDenied_AllEndpoints(t *testing.T) {
	s := newAdminScheduler(t)
	denyAuth := func(*http.Request) bool { return false }
	h := dcron.AdminHandler(denyAuth, s)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/status"},
		{http.MethodPost, "/jobs"},
		{http.MethodPost, "/jobs/alpha/pause"},
		{http.MethodPost, "/jobs/alpha/resume"},
		{http.MethodDelete, "/jobs/alpha"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s auth deny: got %d, want 403", ep.method, ep.path, rr.Code)
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

func TestAdminHandler_AddDynamic(t *testing.T) {
	s := newAdminScheduler(t)
	s.RegisterHandler("dyn-1", func(_ context.Context) error { return nil })
	h := dcron.AdminHandler(nil, s)

	// 1. Success
	body := `{"name":"dyn-1","spec":"@every 1m"}`
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("add dynamic: got %d, want 201", rr.Code)
	}

	// 2. Duplicate returns 409 Conflict
	req = httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("add dynamic duplicate: got %d, want 409", rr.Code)
	}

	// 3. Missing handler returns 400 Bad Request
	missingBody := `{"name":"dyn-no-handler","spec":"@every 1m"}`
	req = httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(missingBody))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("add dynamic missing handler: got %d, want 400", rr.Code)
	}

	// 4. Invalid body returns 400 Bad Request
	req = httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{invalid json`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("add dynamic invalid json: got %d, want 400", rr.Code)
	}
}

func TestRequireBearerToken(t *testing.T) {
	s := newAdminScheduler(t)
	const secret = "super-secret-token-12345"
	h := dcron.AdminHandler(dcron.RequireBearerToken(secret), s)

	tests := []struct {
		name       string
		authHeader string
		wantCode   int
	}{
		{
			name:       "missing header",
			authHeader: "",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "wrong prefix basic auth",
			authHeader: "Basic dXNlcjpwYXNz",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "wrong bearer token",
			authHeader: "Bearer invalid-token",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "prefix collision token",
			authHeader: "Bearer super-secret-token-123",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "extra suffix token",
			authHeader: "Bearer super-secret-token-12345-extra",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "bearer without space",
			authHeader: "Bearersuper-secret-token-12345",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "valid token",
			authHeader: "Bearer super-secret-token-12345",
			wantCode:   http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/status", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Fatalf("auth check %q: got status %d, want %d", tc.name, rr.Code, tc.wantCode)
			}
		})
	}

	t.Run("empty configured token safely rejects all requests", func(t *testing.T) {
		emptyAuthHandler := dcron.AdminHandler(dcron.RequireBearerToken(""), s)

		// Even if client provides empty or non-empty token, it must be rejected
		for _, authVal := range []string{"", "Bearer ", "Bearer something"} {
			req := httptest.NewRequest(http.MethodGet, "/status", nil)
			if authVal != "" {
				req.Header.Set("Authorization", authVal)
			}
			rr := httptest.NewRecorder()
			emptyAuthHandler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("empty token with auth %q: got %d, want 403", authVal, rr.Code)
			}
		}
	})

	t.Run("mutation endpoint requires auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("unauthenticated pause: got %d, want 403", rr.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("authenticated pause: got %d, want 204", rr.Code)
		}
	})
}
