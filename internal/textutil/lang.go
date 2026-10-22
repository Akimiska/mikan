package textutil

import "strings"

// Lang is the language of a text the panel writes for people. One rule for every surface:
// English when the setting says "en", Russian for anything else, "" (auto) and "ru"
// included, the panel's own language.
type Lang string

// En says whether the texts go in English.
func (l Lang) En() bool { return strings.EqualFold(strings.TrimSpace(string(l)), "en") }

// Pick is ru or en by the language.
func (l Lang) Pick(ru, en string) string {
	if l.En() {
		return en
	}
	return ru
}

// ByLang is ru or en by lang: the table of words of either language, a code of it.
func ByLang[T any](lang string, ru, en T) T {
	if Lang(lang).En() {
		return en
	}
	return ru
}
