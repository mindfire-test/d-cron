package dcron_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mindfire-test/d-cron/dcron"
)

// TestAddReturnsTypedErrors asserts that Add returns the typed, context-carrying
// errors for issue #26 while remaining errors.Is-compatible with the existing
// sentinels (so nothing that compared on dcron.ErrJobExists/dcron.ErrInvalidSpec breaks).
func TestAddReturnsTypedErrors(t *testing.T) {
	t.Parallel()
	s := testScheduler(newSchedBackend())
	noop := func(context.Context) error { return nil }

	if err := s.Add("dup", "*/5 * * * *", noop); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := s.Add("dup", "*/5 * * * *", noop)
	if !errors.Is(err, dcron.ErrJobExists) {
		t.Fatalf("duplicate err = %v; want dcron.ErrJobExists", err)
	}
	var je *dcron.JobExistsError
	if !errors.As(err, &je) || je.Name != "dup" {
		t.Fatalf("duplicate err = %v; want *dcron.JobExistsError{Name: dup}", err)
	}

	bad := s.Add("bad", "not a spec", noop)
	if !errors.Is(bad, dcron.ErrInvalidSpec) {
		t.Fatalf("bad spec err = %v; want dcron.ErrInvalidSpec", bad)
	}
	var ise *dcron.InvalidSpecError
	if !errors.As(bad, &ise) || ise.Name != "bad" || ise.Spec != "not a spec" {
		t.Fatalf("bad spec err = %v; want *dcron.InvalidSpecError{Name: bad, Spec: not a spec}", bad)
	}
}
