package dcron_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mindfire-test/d-cron/dcron"
)

func TestAdminHandler_StandbyForwarding_Success(t *testing.T) {
	var leaderPauseCalled atomic.Bool
	var leaderResumeCalled atomic.Bool
	var leaderDeleteCalled atomic.Bool
	var leaderAddCalled atomic.Bool

	// 1. Mock active leader HTTP server
	leaderMux := http.NewServeMux()
	leaderMux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if body["name"] != "dynamic-forwarded" || body["spec"] != "@every 1m" {
			http.Error(w, "unexpected body", http.StatusBadRequest)
			return
		}
		leaderAddCalled.Store(true)
		w.WriteHeader(http.StatusCreated)
	})
	leaderMux.HandleFunc("POST /jobs/{name}/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		leaderPauseCalled.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	leaderMux.HandleFunc("POST /jobs/{name}/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		leaderResumeCalled.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	leaderMux.HandleFunc("DELETE /jobs/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		leaderDeleteCalled.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	leaderServer := httptest.NewServer(leaderMux)
	defer leaderServer.Close()

	// 2. Standby scheduler configured with leader forward URL
	s := testScheduler(standbyBackend{}, dcron.WithLeaderForwardURL(leaderServer.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(func() bool { return s.Leadership() == dcron.LeadershipStandby }, "standby state")

	auth := func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer secret"
	}
	h := dcron.AdminHandler(auth, s)

	// Test Pause forwarded
	req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("pause: got %d, want 204", rr.Code)
	}
	if !leaderPauseCalled.Load() {
		t.Error("pause was not forwarded to leader")
	}

	// Test Resume forwarded
	req = httptest.NewRequest(http.MethodPost, "/jobs/alpha/resume", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("resume: got %d, want 204", rr.Code)
	}
	if !leaderResumeCalled.Load() {
		t.Error("resume was not forwarded to leader")
	}

	// Test Remove forwarded
	req = httptest.NewRequest(http.MethodDelete, "/jobs/alpha", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("remove: got %d, want 204", rr.Code)
	}
	if !leaderDeleteCalled.Load() {
		t.Error("remove was not forwarded to leader")
	}

	// Test AddDynamic (POST /jobs with body) forwarded
	bodyJSON := `{"name":"dynamic-forwarded","spec":"@every 1m"}`
	req = httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("add dynamic: got %d, want 201", rr.Code)
	}
	if !leaderAddCalled.Load() {
		t.Error("add dynamic was not forwarded to leader")
	}
}

func TestAdminHandler_StandbyForwarding_LeaderUnavailable(t *testing.T) {
	// Standby with invalid/unreachable leader URL
	s := testScheduler(standbyBackend{}, dcron.WithLeaderForwardURL("http://127.0.0.1:59999"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(func() bool { return s.Leadership() == dcron.LeadershipStandby }, "standby state")

	h := dcron.AdminHandler(nil, s)
	req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Must fail with 409 Conflict rather than silently succeeding or panicking
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when leader is unavailable, got %d", rr.Code)
	}
}

func TestAdminHandler_StandbyWithoutForwardURL_Returns409(t *testing.T) {
	s := testScheduler(standbyBackend{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(func() bool { return s.Leadership() == dcron.LeadershipStandby }, "standby state")

	h := dcron.AdminHandler(nil, s)
	req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on standby without forward URL, got %d", rr.Code)
	}
}

func TestAdminHandler_AuthRejectedBeforeForwarding(t *testing.T) {
	s := testScheduler(standbyBackend{}, dcron.WithLeaderForwardURL("http://should-not-be-called"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(func() bool { return s.Leadership() == dcron.LeadershipStandby }, "standby state")

	auth := func(*http.Request) bool { return false }
	h := dcron.AdminHandler(auth, s)

	req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for auth failure on standby, got %d", rr.Code)
	}
}

func TestAddDynamic_Deduplication(t *testing.T) {
	b := newSchedBackend()
	s := testScheduler(b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	fn := func(_ context.Context) error { return nil }
	if err := s.AddDynamic("dyn-unique", "@every 1m", fn); err != nil {
		t.Fatalf("AddDynamic first: %v", err)
	}

	// Second registration of same name must fail with ErrJobExists
	err := s.AddDynamic("dyn-unique", "@every 1m", fn)
	if !errors.Is(err, dcron.ErrJobExists) {
		t.Fatalf("expected ErrJobExists on duplicate AddDynamic, got %v", err)
	}
}

func TestAdminHandler_StandbyForwarding_PreservesLeaderError(t *testing.T) {
	leaderMux := http.NewServeMux()
	leaderMux.HandleFunc("POST /jobs/{name}/pause", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "job already paused", http.StatusConflict)
	})
	leaderServer := httptest.NewServer(leaderMux)
	defer leaderServer.Close()

	s := testScheduler(standbyBackend{}, dcron.WithLeaderForwardURL(leaderServer.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(func() bool { return s.Leadership() == dcron.LeadershipStandby }, "standby state")

	h := dcron.AdminHandler(nil, s)
	req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("got status %d; want 409 Conflict preserved from leader", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "job already paused") {
		t.Fatalf("body = %q; want preserved error body from leader", rr.Body.String())
	}
}

func TestAdminHandler_StandbyForwarding_ClientTimeout(t *testing.T) {
	// Leader simulates a slow/hanging endpoint
	leaderMux := http.NewServeMux()
	leaderMux.HandleFunc("POST /jobs/{name}/pause", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	leaderServer := httptest.NewServer(leaderMux)
	defer leaderServer.Close()

	s := testScheduler(standbyBackend{}, dcron.WithLeaderForwardURL(leaderServer.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(func() bool { return s.Leadership() == dcron.LeadershipStandby }, "standby state")

	h := dcron.AdminHandler(nil, s)

	// Client context with a very short timeout
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer reqCancel()

	req := httptest.NewRequest(http.MethodPost, "/jobs/alpha/pause", nil).WithContext(reqCtx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Since client timed out, forwarding should fail and report 409 conflict
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when request context times out, got %d", rr.Code)
	}
}
