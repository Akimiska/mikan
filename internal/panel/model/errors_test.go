package model

import (
	"fmt"
	"testing"
)

// Every error the bot's RPC carries by a code comes back as itself: ErrorOf is CodeOf's
// inverse, one code for an error and one error for a code. Wrapped, an error crosses by the
// same code, but ErrProviderOff: wrapped, the provider failed while it was asked.
func TestCodesComeBack(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c.code] {
			t.Errorf("%s stands for two errors", c.code)
		}
		seen[c.code] = true
		if got := CodeOf(c.err); got != c.code {
			t.Errorf("%v crosses as %q, want %q", c.err, got, c.code)
		}
		if got := ErrorOf(c.code); got != c.err {
			t.Errorf("%s comes back as %v, want %v", c.code, got, c.err)
		}
		want := c.code
		if c.err == ErrProviderOff {
			want = ""
		}
		if got := CodeOf(fmt.Errorf("asked: %w", c.err)); got != want {
			t.Errorf("%v wrapped crosses as %q, want %q", c.err, got, want)
		}
	}
	if CodeOf(fmt.Errorf("something else")) != "" || ErrorOf("something_else") != nil {
		t.Fatal("an error of no code crosses by one")
	}
}

// Every refusal Refusal tells has a code to cross the bot's RPC with, and comes back from it
// as itself, told the same: the bot tells the buyer what the Mini App does.
func TestRefusalsCross(t *testing.T) {
	for _, err := range append(append([]error{}, refusals...), ErrPromo, ErrProviderOff) {
		code := CodeOf(err)
		if code == "" {
			t.Errorf("the refusal %v has no code", err)
			continue
		}
		back := ErrorOf(code)
		want, _ := Refusal(err)
		if got, known := Refusal(back); back != err || got != want || !known {
			t.Errorf("the refusal %v came back as %v, told %q (%v), want %q", err, back, got, known, want)
		}
	}
}
