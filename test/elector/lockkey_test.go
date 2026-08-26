package elector_test

import (
	"testing"

	"github.com/mindfire-test/d-cron/internal/elector"
)

func TestLockKeyDeterministic(t *testing.T) {
	a := elector.LockKey("billing")
	if a == 0 {
		t.Fatalf("elector.LockKey must never be zero")
	}
	if b := elector.LockKey("billing"); b != a {
		t.Fatalf("elector.LockKey not deterministic: %d != %d", b, a)
	}
	if c := elector.LockKey("notifications"); c == a {
		t.Fatalf("different namespaces must yield different keys")
	}
}

func TestLockKeyEmptyAndUnicode(t *testing.T) {
	for _, ns := range []string{"", "default", "ümlaut-namespace", "🎉", "\x00control"} {
		if got := elector.LockKey(ns); got == 0 {
			t.Errorf("elector.LockKey(%q) = 0; must never be zero", ns)
		}
		if again := elector.LockKey(ns); again != elector.LockKey(ns) {
			t.Errorf("elector.LockKey(%q) not deterministic", ns)
		}
	}
	if elector.LockKey("") == elector.LockKey("default") {
		t.Fatal("empty namespace must not collide with the default namespace")
	}
}
