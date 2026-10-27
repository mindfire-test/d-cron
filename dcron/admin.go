package dcron

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// AuthFunc is an optional guard called before every admin request.
// It returns true if the request is authorized, or false to reject it with 403 Forbidden.
type AuthFunc func(r *http.Request) bool

// RequireBearerToken returns an AuthFunc that validates incoming requests against the provided
// static token using constant-time comparison. Requests must provide an "Authorization" header
// matching "Bearer <token>".
//
// If token is empty, the returned AuthFunc safely rejects all requests.
func RequireBearerToken(token string) AuthFunc {
	return func(r *http.Request) bool {
		if token == "" {
			return false
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return false
		}
		provided := strings.TrimPrefix(authHeader, "Bearer ")
		return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
	}
}

// AdminHandler returns an http.Handler that exposes a minimal management API for the scheduler.
//
// SECURITY WARNING:
// If auth is nil, all endpoints (/status, /jobs/{name}/pause, /jobs/{name}/resume,
// /jobs/{name}/run, /jobs/{name}, /jobs) are completely unauthenticated.
// Any client with network reachability to the server can inspect schedules, pause or remove
// existing jobs, trigger manual runs, or inject arbitrary dynamic jobs.
// In production environments, always pass a non-nil AuthFunc (such as RequireBearerToken)
// or place this handler behind an authentication reverse proxy / TLS gateway.
func AdminHandler(auth AuthFunc, s *Scheduler) http.Handler {
	mux := http.NewServeMux()

	// GET /status
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		if auth != nil && !auth(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		type jobJSON struct {
			Name        string    `json:"name"`
			Spec        string    `json:"spec"`
			NextRun     time.Time `json:"next_run,omitempty"`
			LastRun     time.Time `json:"last_run,omitempty"`
			LastOutcome string    `json:"last_outcome,omitempty"`
			LastError   string    `json:"last_error,omitempty"`
			Running     bool      `json:"running"`
			Paused      bool      `json:"paused"`
		}
		type statusJSON struct {
			Instance  string    `json:"instance"`
			Namespace string    `json:"namespace"`
			Leader    bool      `json:"is_leader"`
			Jobs      []jobJSON `json:"jobs"`
		}
		jobs := s.Jobs()
		jj := make([]jobJSON, 0, len(jobs))
		for _, j := range jobs {
			jj = append(jj, jobJSON{
				Name:        j.Name,
				Spec:        j.Spec,
				NextRun:     j.NextRun,
				LastRun:     j.LastRun,
				LastOutcome: j.LastOutcome,
				LastError:   j.LastError,
				Running:     j.Running,
				Paused:      j.Paused,
			})
		}
		resp := statusJSON{
			Instance:  s.InstanceID(),
			Namespace: s.Namespace(),
			Leader:    s.leader.IsLeader(),
			Jobs:      jj,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// POST /jobs (AddDynamic)
	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		if auth != nil && !auth(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if s.started && s.Leadership() == LeadershipStandby {
			if s.opts.leaderForwardURL != "" {
				forwardToLeader(w, r, s.opts.leaderForwardURL)
				return
			}
			http.Error(w, ErrNotLeader.Error(), http.StatusConflict)
			return
		}
		type dynamicJobReq struct {
			Name string `json:"name"`
			Spec string `json:"spec"`
		}
		var req dynamicJobReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Spec == "" {
			http.Error(w, "name and spec are required", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		fn, hasFn := s.handlers[req.Name]
		s.mu.Unlock()
		if !hasFn || fn == nil {
			http.Error(w, "no registered handler for job: "+req.Name, http.StatusBadRequest)
			return
		}
		if err := s.AddDynamic(req.Name, req.Spec, fn); err != nil {
			if errors.Is(err, ErrNotLeader) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if errors.Is(err, ErrJobExists) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	handleMutation := func(w http.ResponseWriter, r *http.Request, suffix string, mutate func(string) error) {
		if auth != nil && !auth(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if s.started && s.Leadership() == LeadershipStandby {
			if s.opts.leaderForwardURL != "" {
				forwardToLeader(w, r, s.opts.leaderForwardURL)
				return
			}
			http.Error(w, ErrNotLeader.Error(), http.StatusConflict)
			return
		}
		name := jobNameFromPath(r.URL.Path, suffix)
		if err := mutate(name); err != nil {
			if errors.Is(err, ErrNotLeader) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if errors.Is(err, ErrJobNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}

	// POST /jobs/{name}/pause
	mux.HandleFunc("POST /jobs/{name}/pause", func(w http.ResponseWriter, r *http.Request) {
		handleMutation(w, r, "/pause", s.Pause)
	})

	// POST /jobs/{name}/resume
	mux.HandleFunc("POST /jobs/{name}/resume", func(w http.ResponseWriter, r *http.Request) {
		handleMutation(w, r, "/resume", s.Resume)
	})

	// DELETE /jobs/{name}
	mux.HandleFunc("DELETE /jobs/{name}", func(w http.ResponseWriter, r *http.Request) {
		if auth != nil && !auth(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if s.started && s.Leadership() == LeadershipStandby {
			if s.opts.leaderForwardURL != "" {
				forwardToLeader(w, r, s.opts.leaderForwardURL)
				return
			}
			http.Error(w, ErrNotLeader.Error(), http.StatusConflict)
			return
		}
		name := jobNameFromPath(r.URL.Path, "")
		name = strings.TrimPrefix(name, "/jobs/")
		if err := s.Remove(name); err != nil {
			if errors.Is(err, ErrNotLeader) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if errors.Is(err, ErrJobNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

// forwardToLeader proxies an admin HTTP request to the active leader instance.
func forwardToLeader(w http.ResponseWriter, r *http.Request, leaderURL string) {
	targetURL := strings.TrimRight(leaderURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "dcron: failed to create proxy request", http.StatusInternalServerError)
		return
	}
	for k, vv := range r.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, "dcron: leader unavailable: "+err.Error(), http.StatusConflict)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// jobNameFromPath extracts the job name from the URL path by stripping the
// leading "/jobs/" prefix and an optional trailing suffix (e.g. "/pause").
func jobNameFromPath(path, suffix string) string {
	path = strings.TrimPrefix(path, "/jobs/")
	path = strings.TrimSuffix(path, suffix)
	return path
}
