package metrics_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mindfire-test/d-cron/metrics"
)

// TestCoreDoesNotLinkMetricsSDK enforces NFR-402 / D-08: the scheduler core
// (dcron + internal/*) must never import a metrics SDK. The seam is the
// Recorder interface in this package; an application bridges it to whatever
// registry it owns. If this test fails, observability leaked into the core.
func TestCoreDoesNotLinkMetricsSDK(t *testing.T) {
	t.Parallel()
	banned := []string{"prometheus/client_golang", "go.opentelemetry.io", "github.com/prometheus"}
	roots := []string{"../../dcron", "../../internal"}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(src), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "import") && !strings.HasPrefix(line, "\"") {
					continue
				}
				for _, b := range banned {
					if strings.Contains(line, b) {
						t.Errorf("%s: core imports %q (NFR-402 violation)", path, b)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}

func TestOutcomeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome metrics.Outcome
		want    string
	}{
		{metrics.OutcomeOK, "success"},
		{metrics.OutcomeFailed, "failed"},
		{metrics.OutcomePanicked, "panicked"},
		{metrics.OutcomeTimedOut, "timeout"},
		{metrics.OutcomeCanceled, "canceled"},
		{metrics.OutcomeSkipped, "skipped"},
		{metrics.OutcomeUnknown, "unknown"},
		{metrics.Outcome(255), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.outcome.String(); got != tt.want {
			t.Errorf("Outcome(%d).String() = %q; want %q", tt.outcome, got, tt.want)
		}
	}
}

func TestNoopRecorder(t *testing.T) {
	t.Parallel()

	var n metrics.Noop
	n.SetLeader("inst-1", true)
	n.SetLeader("inst-1", false)
	n.LeaderTransition("inst-1")
	n.JobStarted("job-1")
	n.JobFinished("job-1", metrics.OutcomeOK, 100, true)
	n.FencedWrite()
	n.MissedRun("job-1")
}
