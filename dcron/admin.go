package dcron

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// AuthFunc is an optional guard called before every admin request. It receives
// the raw *http.Request and must return true to allow the request through.
// Return false (or call http.Error inside the function) to deny access.
// When nil is passed to AdminHandler all requests are allowed — only use nil
// behind an already-authenticated reverse proxy (NFR-502).
type AuthFunc func(r *http.Request) bool

// AdminHandler returns an http.Handler that exposes a minimal management API
// for the scheduler (issue #51, FR-605). Mount it anywhere in your application:
//
//	mux.Handle("/admin/dcron/", http.StripPrefix("/admin/dcron", dcron.AdminHandler(myAuth, scheduler)))
//
// Endpoints:
//
//	GET  /status                   — scheduler and job list snapshot
//	POST /jobs/{name}/pause        — pause a running job
//	POST /jobs/{name}/resume       — resume a paused job
//	DELETE /jobs/{name}            — remove a job at runtime
//
// The API is disabled by default. The host application is responsible for
// authentication; pass an [AuthFunc] that checks credentials, tokens, or IP
// allowlists. Passing nil grants access to all callers.
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

	// POST /jobs/{name}/pause
	mux.HandleFunc("POST /jobs/{name}/pause", func(w http.ResponseWriter, r *http.Request) {
		if auth != nil && !auth(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name := jobNameFromPath(r.URL.Path, "/pause")
		if err := s.Pause(name); err != nil {
			if errors.Is(err, ErrJobNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// POST /jobs/{name}/resume
	mux.HandleFunc("POST /jobs/{name}/resume", func(w http.ResponseWriter, r *http.Request) {
		if auth != nil && !auth(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name := jobNameFromPath(r.URL.Path, "/resume")
		if err := s.Resume(name); err != nil {
			if errors.Is(err, ErrJobNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// DELETE /jobs/{name}
	mux.HandleFunc("DELETE /jobs/{name}", func(w http.ResponseWriter, r *http.Request) {
		if auth != nil && !auth(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name := jobNameFromPath(r.URL.Path, "")
		// Strip leading "/jobs/" prefix manually since the path may include it.
		name = strings.TrimPrefix(name, "/jobs/")
		if err := s.Remove(name); err != nil {
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

// jobNameFromPath extracts the job name from the URL path by stripping the
// leading "/jobs/" prefix and an optional trailing suffix (e.g. "/pause").
func jobNameFromPath(path, suffix string) string {
	path = strings.TrimPrefix(path, "/jobs/")
	path = strings.TrimSuffix(path, suffix)
	return path
}
