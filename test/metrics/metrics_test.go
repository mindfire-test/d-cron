package metrics_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoreDoesNotLinkMetricsSDK enforces NFR-402 / D-08: the scheduler core
// (dcron + internal/*) must never import a metrics SDK. The seam is the
// Recorder interface in this package; an application bridges it to whatever
// registry it owns. If this test fails, observability leaked into the core.
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
