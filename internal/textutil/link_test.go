package textutil

import (
	"strings"
	"testing"
)

func TestValidLink(t *testing.T) {
	https := LinkRules{}
	apps := LinkRules{TG: true, Opaque: true}
	page := LinkRules{HTTP: true, TG: true, Opaque: true, MaxLen: 500}
	text := LinkRules{HTTP: true, TG: true, MaxLen: 2048}
	for _, c := range []struct {
		s     string
		rules LinkRules
		ok    bool
	}{
		{"https://t.me/news", https, true},
		{"https://t.me/news", apps, true},
		{"https://t.me/a b", apps, false},
		{"https://t.me/a\tb", apps, false},
		{"https://t.me/a\x00", apps, false},
		{"https://t.me/a\x7f", apps, false},
		{"https://", apps, false},
		{"", apps, false},
		{"https://user:pass@t.me/news", apps, false},
		{"http://t.me/news", https, false},
		{"http://t.me/news", apps, false},
		{"http://t.me/news", page, true},
		{"http://t.me/news", text, true},
		{"http://", page, false},
		{"tg://resolve?domain=news", https, false},
		{"tg://resolve?domain=news", apps, true},
		{"tg://resolve?domain=news", text, true},
		{"tg:resolve?domain=news", apps, true},
		{"tg:resolve?domain=news", text, false}, // a bot's button needs the slashes
		{"tg://user@resolve", apps, false},
		{"tg:", apps, false},
		{"javascript:alert(1)", page, false},
		{"//t.me/news", page, false},
		{"https://t.me/" + strings.Repeat("a", 500-len("https://t.me/")), page, true},
		{"https://t.me/" + strings.Repeat("a", 501-len("https://t.me/")), page, false},
		{"https://t.me/" + strings.Repeat("a", 5000), apps, true}, // no limit by default
		{"https://t.me/" + strings.Repeat("a", 2048-len("https://t.me/")), text, true},
		{"https://t.me/" + strings.Repeat("a", 2049-len("https://t.me/")), text, false},
	} {
		if got := ValidLink(c.s, c.rules); got != c.ok {
			t.Errorf("ValidLink(%.60q, %+v) = %v", c.s, c.rules, got)
		}
	}
}
