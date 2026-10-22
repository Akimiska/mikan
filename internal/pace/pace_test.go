package pace

import (
	"context"
	"math"
	"testing"
	"time"
)

// returns runs fn and fails the test when it does not come back: a wait that ignores ctx
// would hang the whole run.
func returns(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after ctx ended")
	}
}

func TestBackoffNext(t *testing.T) {
	s, ms := time.Second, time.Millisecond
	const maxDuration = time.Duration(math.MaxInt64)
	for _, c := range []struct {
		name string
		b    Backoff
		n    int
		want time.Duration
	}{
		{"a negative count is the first failure", Backoff{Base: s, Max: time.Minute}, -5, s},
		{"the first failure", Backoff{Base: s, Max: time.Minute}, 0, s},
		{"doubled once", Backoff{Base: s, Max: time.Minute}, 1, 2 * s},
		{"doubled twice", Backoff{Base: s, Max: time.Minute}, 2, 4 * s},
		{"doubled five times", Backoff{Base: s, Max: time.Minute}, 5, 32 * s},
		{"the sixth doubling hits the cap", Backoff{Base: s, Max: time.Minute}, 6, time.Minute},
		{"it stays at the cap", Backoff{Base: s, Max: time.Minute}, 7, time.Minute},
		{"a long run of failures", Backoff{Base: s, Max: time.Minute}, 100, time.Minute},
		{"the largest count", Backoff{Base: s, Max: time.Minute}, math.MaxInt, time.Minute},

		// The schedules the panel's retries had written out by hand.
		{"node retry, first", Backoff{Base: 5 * s, Max: 10 * time.Minute}, 0, 5 * s},
		{"node retry, seventh", Backoff{Base: 5 * s, Max: 10 * time.Minute}, 6, 320 * s},
		{"node retry, eighth", Backoff{Base: 5 * s, Max: 10 * time.Minute}, 7, 10 * time.Minute},
		{"node retry, ninth", Backoff{Base: 5 * s, Max: 10 * time.Minute}, 8, 10 * time.Minute},
		{"database start, fifth", Backoff{Base: 250 * ms, Max: 5 * s}, 4, 4 * s},
		{"database start, sixth", Backoff{Base: 250 * ms, Max: 5 * s}, 5, 5 * s},
		{"transaction conflict, sixth", Backoff{Base: 5 * ms, Max: 250 * ms}, 5, 160 * ms},
		{"transaction conflict, seventh", Backoff{Base: 5 * ms, Max: 250 * ms}, 6, 250 * ms},

		{"the cap is the base", Backoff{Base: time.Hour, Max: time.Hour}, 3, time.Hour},
		{"a base above the cap is the first pause as it is", Backoff{Base: time.Hour, Max: time.Minute}, 0, time.Hour},
		{"and the cap after it", Backoff{Base: time.Hour, Max: time.Minute}, 1, time.Minute},
		{"no base, no pause", Backoff{Max: time.Minute}, math.MaxInt, 0},

		{"no cap doubles on", Backoff{Base: s}, 3, 8 * s},
		{"no cap, last doubling that fits", Backoff{Base: s}, 33, s << 33},
		{"no cap, one more stops at the largest duration", Backoff{Base: s}, 34, maxDuration},
		{"no cap, a long run of failures", Backoff{Base: s}, math.MaxInt, maxDuration},
		{"a negative cap is no cap", Backoff{Base: s, Max: -s}, 3, 8 * s},
		{"a cap of the largest duration", Backoff{Base: s, Max: maxDuration}, 1000, maxDuration},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.b.Next(c.n); got != c.want {
				t.Fatalf("%+v.Next(%d) = %s, want %s", c.b, c.n, got, c.want)
			}
		})
	}
}

// The pause never shrinks as failures pile up, never passes the cap and never wraps round
// to a negative one.
func TestBackoffNextStaysWithinBounds(t *testing.T) {
	for _, b := range []Backoff{
		{Base: time.Millisecond, Max: time.Second},
		{Base: 5 * time.Second, Max: 10 * time.Minute},
		{Base: 3 * time.Nanosecond, Max: time.Duration(math.MaxInt64 - 1)},
		{Base: time.Second, Max: time.Duration(math.MaxInt64)},
		{Base: time.Second},
	} {
		prev := b.Base
		for n := 0; n <= 200; n++ {
			got := b.Next(n)
			if got < prev || b.Max > 0 && got > b.Max {
				t.Fatalf("%+v.Next(%d) = %s after %s", b, n, got, prev)
			}
			prev = got
		}
	}
}

func TestSleepWaitsTheTime(t *testing.T) {
	start := time.Now()
	if !Sleep(context.Background(), 30*time.Millisecond) {
		t.Fatal("Sleep gave up though ctx did not end")
	}
	if got := time.Since(start); got < 30*time.Millisecond {
		t.Fatalf("woke after %s, before the time was up", got)
	}
}

func TestSleepStopsWhenCtxEnds(t *testing.T) {
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	meanwhile, stop := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, stop)
	for name, ctx := range map[string]context.Context{"before": ended, "meanwhile": meanwhile} {
		t.Run(name, func(t *testing.T) {
			var slept bool
			returns(t, func() { slept = Sleep(ctx, time.Hour) })
			if slept {
				t.Fatal("Sleep says the time went by, but ctx ended first")
			}
		})
	}
}

func TestTickCallsAfterEachIntervalUntilCtxEnds(t *testing.T) {
	const every = 40 * time.Millisecond
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "mine"))
	defer cancel()
	var (
		calls int
		first time.Duration
		start = time.Now()
	)
	returns(t, func() {
		Tick(ctx, every, func(ctx context.Context) {
			if calls == 0 {
				first = time.Since(start)
			}
			if ctx.Value(key{}) != "mine" {
				t.Error("fn was not given the ctx Tick runs on")
			}
			if calls++; calls == 3 {
				cancel()
			}
		})
	})
	// One more may come: the cancel and the next tick can be ready together.
	if calls < 3 {
		t.Fatalf("%d calls before ctx ended, want 3", calls)
	}
	if first < every {
		t.Fatalf("first call after %s, want it no sooner than %s", first, every)
	}
	if got := time.Since(start); got < 3*every {
		t.Fatalf("three calls took %s, want at least %s", got, 3*every)
	}
}

func TestTickEndsWhileWaitingWithoutCalling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	var called bool
	returns(t, func() { Tick(ctx, time.Hour, func(context.Context) { called = true }) })
	if called {
		t.Fatal("fn ran before the first interval was up")
	}
}

func TestJitter(t *testing.T) {
	for _, d := range []time.Duration{-time.Second, 0} {
		if got := Jitter(d); got != 0 {
			t.Fatalf("Jitter(%s) = %s, want 0", d, got)
		}
	}
	if got := Jitter(1); got != 0 {
		t.Fatalf("Jitter(1ns) = %s, want 0", got)
	}
	seen := map[time.Duration]bool{}
	for range 200 {
		got := Jitter(time.Hour)
		if got < 0 || got >= time.Hour {
			t.Fatalf("Jitter(1h) = %s, want [0, 1h)", got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatal("Jitter gave the same time every time")
	}
}
