package aoiref

import "testing"

func TestPlural(t *testing.T) {
	for n, want := range map[int64]string{1: "1 день", 2: "2 дня", 5: "5 дней", 7: "7 дней", 11: "11 дней", 21: "21 день", 14: "14 дней", 22: "22 дня"} {
		if got := plural(n); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestNewCode(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c := newCode(6)
		if !codeRe.MatchString(c) || len(c) != 6 {
			t.Fatalf("bad code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes repeat too often: %d distinct of 200", len(seen))
	}
}
