package dcron

import (
	"context"
	"errors"
	"testing"
)

// TestAddReturnsTypedErrors asserts that Add returns the typed, context-carrying
// errors for issue #26 while remaining errors.Is-compatible with the existing
// sentinels (so nothing that compared on ErrJobExists/ErrInvalidSpec breaks).
func TestAddReturnsTypedErrors(t *testing.T) {
	t.Parallel()
	s := newWithBackend(newSchedBackend(), testCfg())
	noop := func(context.Context) error { return nil }

	if err := s.Add("dup", "*/5 * * * *", noop); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := s.Add("dup", "*/5 * * * *", noop)
	if !errors.Is(err, ErrJobExists) {
		t.Fatalf("duplicate err = %v; want ErrJobExists", err)
	}
	var je *JobExistsError
	if !errors.As(err, &je) || je.Name != "dup" {
		t.Fatalf("duplicate err = %v; want *JobExistsError{Name: dup}", err)
	}

	bad := s.Add("bad", "not a spec", noop)
	if !errors.Is(bad, ErrInvalidSpec) {
		t.Fatalf("bad spec err = %v; want ErrInvalidSpec", bad)
	}
	var ise *InvalidSpecError
	if !errors.As(bad, &ise) || ise.Name != "bad" || ise.Spec != "not a spec" {
		t.Fatalf("bad spec err = %v; want *InvalidSpecError{Name: bad, Spec: not a spec}", bad)
	}
}
