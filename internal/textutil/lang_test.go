package textutil

import "testing"

func TestLang(t *testing.T) {
	for lang, en := range map[string]bool{"en": true, "EN": true, " en ": true, "ru": false, "": false, "auto": false, "de": false} {
		if got := Lang(lang).En(); got != en {
			t.Errorf("Lang(%q).En() = %v", lang, got)
		}
		if got, want := Lang(lang).Pick("да", "yes"), map[bool]string{true: "yes", false: "да"}[en]; got != want {
			t.Errorf("Lang(%q).Pick = %q", lang, got)
		}
		if got, want := ByLang(lang, 1, 2), map[bool]int{true: 2, false: 1}[en]; got != want {
			t.Errorf("ByLang(%q) = %d", lang, got)
		}
	}
}
