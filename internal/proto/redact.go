package proto

import "strings"

// Hidden stands in for a secret of a template that its reader may not see: Redact puts it
// in, Restore puts the saved value back when such a template comes back to be saved.
const Hidden = "<hidden>"

// secrets are the values of a template that are credentials: the server's own keys
// (REALITY's, VLESS Encryption's), the key a protocol of one key for everyone gives every
// user, and Hysteria2's obfuscation password. The rest (paths, short ids, server names)
// goes to every subscriber anyway and lets nobody in.
var secrets = [][]string{{"reality-config", "private-key"}, {"decryption"}, {"password"}, {"psk"}, {"key"}, {"obfs-password"}}

// at is the map holding the last key of path in t, and that key; false when the path is
// not there.
func at(t Template, path []string) (map[string]any, string, bool) {
	m := map[string]any(t)
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			return nil, "", false
		}
		m = next
	}
	last := path[len(path)-1]
	_, ok := m[last]
	return m, last, ok
}

// Redact is the template with its secrets as Hidden; one without secrets is given as it
// is. A template that does not parse is given as nothing: what it holds is not known.
func Redact(src string) string {
	t, err := Parse(src)
	if err != nil {
		return ""
	}
	hidden := false
	for _, p := range secrets {
		if m, k, ok := at(t, p); ok && m[k] != "" && m[k] != nil {
			m[k], hidden = Hidden, true
		}
	}
	if !hidden {
		return src
	}
	return Marshal(t)
}

// Restore puts back the secrets a reader got as Hidden, from the template saved before
// (old). A value the reader typed anew stays; a Hidden with nothing saved to take stays as
// it is, for the checks to refuse.
func Restore(src, old string) string {
	if !strings.Contains(src, Hidden) {
		return src
	}
	t, err := Parse(src)
	if err != nil {
		return src
	}
	o, err := Parse(old)
	if err != nil {
		return src
	}
	for _, p := range secrets {
		m, k, ok := at(t, p)
		if !ok || m[k] != Hidden {
			continue
		}
		if om, _, ok := at(o, p); ok {
			m[k] = om[k]
		}
	}
	return Marshal(t)
}
