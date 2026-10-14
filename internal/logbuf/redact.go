package logbuf

import (
	"regexp"
	"strings"
)

// Mark is what takes the place of a secret.
const Mark = "[redacted]"

// sensitive are the names whose values are secrets, in a line of a log and in JSON: tokens,
// passwords, keys, cookies, the ACME key authorization. The panel's own words for them
// (join key, sub token) are in as well.
const sensitive = `(?:token|secret|passw(?:or)?d|passphrase|api[_-]?key|private[_-]?key|join[_-]?key|authorization|cookie|credential|csrf|signature|psk|key[_-]?auth|sub[_-]?(?:token|link|url)|\bkey)`

var rules = []struct {
	re   *regexp.Regexp
	repl string
	// keep, when set, decides per match whether it is a secret (true: replace).
	keep func(match string) bool
}{
	{re: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), repl: "[redacted private key]"},
	// Share links carry the credentials of a protocol, and subscription links the way into a
	// user's profile.
	{re: regexp.MustCompile(`(?i)\b(vless|vmess|trojan|ssr?|hysteria2?|hy2|tuic|anytls|wireguard|socks5?|mieru|trusttunnel|clash|happ|v2rayng|sing-box|singbox)://[^\s"'<>\\]+`), repl: "${1}://" + Mark},
	// user:password@ in a URL.
	{re: regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://)[^\s/@:"']+:[^\s/@"']+@`), repl: "${1}" + Mark + "@"},
	{re: regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{6,}`), repl: "${1} " + Mark},
	{re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`), repl: Mark},
	// name=value, name="value" and name: value of a line of text.
	{re: regexp.MustCompile(`(?i)(\b[\w.-]*` + sensitive + `[\w.-]*)(\s*[=:]\s*)("(?:[^"\\]|\\.)*"|'[^']*'|[^\s,;&}"']+)`), repl: "${1}${2}" + Mark},
	// "name": "value" of JSON, also inside a quoted slog value (\"name\":\"value\").
	{re: regexp.MustCompile(`(?i)(\\?"[\w.-]*` + sensitive + `[\w.-]*\\?"\s*:\s*)(\\?"(?:[^"\\]|\\.)*?\\?"|[^\s,}\]]+)`), repl: "${1}\"" + Mark + "\""},
	// The tokens the panel hands out: API keys, support access.
	{re: regexp.MustCompile(`\b(ms|mk|mp)_[A-Za-z0-9]{12,}`), repl: "${1}_" + Mark},
	// A Telegram bot's token, which is also in the URL of every Bot API call.
	{re: regexp.MustCompile(`\d{6,12}:[A-Za-z0-9_-]{30,}`), repl: "[redacted bot token]"},
	// A WireGuard or WARP key: 32 bytes of base64.
	{re: regexp.MustCompile(`\b[A-Za-z0-9+/]{43}=`), repl: Mark},
	// The credentials of a protocol: a user's UUID.
	{re: regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), repl: "[uuid]"},
	// A random-looking segment of a path or URL: the panel's secret paths and subscription
	// tokens. Words made of letters only (file and unit names) are not.
	{re: regexp.MustCompile(`/[A-Za-z0-9_-]{20,}`), repl: "/" + Mark, keep: randomSegment},
	// A long random-looking word on its own: a key printed bare. Hashes (all hex) and words
	// of letters only are left.
	{re: regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}`), repl: Mark, keep: randomWord},
}

// randomWord says whether a long word looks like a key: digits and letters, and not a hash.
func randomWord(m string) bool {
	var digit, letter, nonHex bool
	for _, c := range m {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			letter = true
		case c >= 'g' && c <= 'z', c >= 'G' && c <= 'Z':
			letter, nonHex = true, true
		default:
			nonHex = true // _ and -
		}
	}
	return digit && letter && nonHex
}

// randomSegment says whether a "/segment" looks like a token: long, with both digits and
// letters in it.
func randomSegment(m string) bool {
	var digit, letter bool
	for _, c := range m[1:] {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			letter = true
		}
	}
	return digit && letter
}

// Redact cuts the secrets out of a line of a log: tokens and API keys, passwords, cookies
// and bearer headers, share and subscription links, private keys, UUIDs and the random
// segments of paths. It errs toward cutting too much: a log is read by someone who is
// not the admin.
func Redact(s string) string {
	for _, r := range rules {
		if r.keep == nil {
			s = r.re.ReplaceAllString(s, r.repl)
			continue
		}
		s = r.re.ReplaceAllStringFunc(s, func(m string) string {
			if r.keep(m) {
				return r.repl
			}
			return m
		})
	}
	return s
}

// RedactWords cuts out the words that Redact cannot know: values only the caller has
// (the panel's secret paths). Words shorter than four characters are left, so that a
// setting nobody filled in does not blank the log.
func RedactWords(s string, words []string) string {
	for _, w := range words {
		if len(w) >= 4 {
			s = strings.ReplaceAll(s, w, Mark)
		}
	}
	return s
}
