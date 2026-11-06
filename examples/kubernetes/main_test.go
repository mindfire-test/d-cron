package main

import (
	"database/sql"
	"testing"
)

func TestPostgresDriverRegistered(t *testing.T) {
	t.Parallel()

	drivers := sql.Drivers()
	found := false
	for _, d := range drivers {
		if d == "postgres" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'postgres' driver to be registered in sql.Drivers(), got: %v", drivers)
	}
}
