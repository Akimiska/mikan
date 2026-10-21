package textutil

import (
	"net/url"
	"strings"
)

// LinkRules say which links ValidLink takes. The zero value takes https alone, which is
// what a picture or a page of the admin's may be.
type LinkRules struct {
	HTTP   bool // plain http as well
	TG     bool // tg://host links as well
	Opaque bool // and tg:host?x=y, with no slashes; only with TG
	MaxLen int  // the longest link in bytes; 0 for any length
}

// ValidLink says whether s is a link to put in front of a person: an address with a host and
// no user name in it, https or what rules add, with no space or control character.
func ValidLink(s string, rules LinkRules) bool {
	if s == "" || rules.MaxLen > 0 && len(s) > rules.MaxLen || strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		return rules.HTTP && u.Host != ""
	case "tg":
		return rules.TG && (u.Host != "" || rules.Opaque && u.Opaque != "")
	}
	return false
}
