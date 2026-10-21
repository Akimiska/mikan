package tgbot

import "testing"

// render is one pass: a value that itself holds {something} is not filled in again, and
// the result does not depend on the order the map is walked in.
func TestRenderIsOnePassAndDeterministic(t *testing.T) {
	vars := map[string]string{"name": "{used} <b>", "used": "5 GB", "brand": "Mikan", "left": "1 GB"}
	want := "Mikan: {used} &lt;b&gt;, 5 GB, {unknown}, {open"
	for range 200 {
		if got := render("{brand}: {name}, {used}, {unknown}, {open", vars); got != want {
			t.Fatalf("render: %q, want %q", got, want)
		}
	}
	if got := render("a < b {brand}", vars); got != "a &lt; b Mikan" {
		t.Fatalf("the text is escaped: %q", got)
	}
}
