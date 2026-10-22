package textutil

import "strings"

// Fill puts vars in place of the {names} in text. One pass: a value that itself holds
// {something} is not filled again, and the result does not depend on the order the
// variables are listed in. A {name} that is not in vars stays as it is. escape (nil: none)
// is applied to the text and to every value, for a place where both must be safe to put in.
func Fill(text string, vars map[string]string, escape func(string) string) string {
	if escape == nil {
		escape = func(s string) string { return s }
	}
	rest := escape(text)
	var out strings.Builder
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			break
		}
		if v, ok := vars[rest[i+1:i+j]]; ok {
			out.WriteString(rest[:i])
			out.WriteString(escape(v))
			rest = rest[i+j+1:]
			continue
		}
		out.WriteString(rest[:i+1])
		rest = rest[i+1:]
	}
	out.WriteString(rest)
	return out.String()
}
