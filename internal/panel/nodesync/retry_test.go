package nodesync

import (
	"errors"
	"testing"
	"time"
)

// The pause after a failed attempt at a node doubles from five seconds to ten minutes and
// stays there, however long the node is down; the first success starts it over.
func TestRetryPauseDoublesToTheCap(t *testing.T) {
	var r retry
	now := time.Unix(1_000_000, 0)
	err := errors.New("down")
	for i, seconds := range []time.Duration{5, 10, 20, 40, 80, 160, 320, 600, 600, 600} {
		r.fail(now, err)
		if got := r.until.Sub(now); got != seconds*time.Second {
			t.Fatalf("pause after failure %d = %s, want %s", i+1, got, seconds*time.Second)
		}
	}
	for range 200 {
		r.fail(now, err)
	}
	if got := r.until.Sub(now); got != retryMax {
		t.Fatalf("pause after a long outage = %s, want %s", got, retryMax)
	}
	if !r.ok() {
		t.Fatal("a success after failures was not reported")
	}
	r.fail(now, err)
	if got := r.until.Sub(now); got != retryFirst {
		t.Fatalf("pause after a success = %s, want %s", got, retryFirst)
	}
}
