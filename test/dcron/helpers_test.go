package dcron_test

import (
	"time"
)

func waitFor(cond func() bool, what string) {
	deadline := time.Now().Add(4 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		panic("timed out waiting for " + what)
	}
}
