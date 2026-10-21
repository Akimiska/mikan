package textutil

import (
	"html"
	"testing"
)

func TestFill(t *testing.T) {
	vars := map[string]string{"name": "Ann", "tag": "{name}", "x": "<b>&"}
	for _, c := range []struct {
		in     string
		escape func(string) string
		want   string
	}{
		{"Hi {name}!", nil, "Hi Ann!"},
		{"{name}{name}", nil, "AnnAnn"},
		{"{unknown} stays", nil, "{unknown} stays"},
		{"{name", nil, "{name"},
		{"name}", nil, "name}"},
		{"{{name}}", nil, "{Ann}"},
		{"{ {name}", nil, "{ Ann"},
		{"", nil, ""},
		// A value is not filled again: "{tag}" gives its text, "{name}", as it is.
		{"{tag}", nil, "{name}"},
		{"a <i> {x}", nil, "a <i> <b>&"},
		{"a <i> {x}", html.EscapeString, "a &lt;i&gt; &lt;b&gt;&amp;"},
		{"{tag}", html.EscapeString, "{name}"},
		{"Tom & {name}", html.EscapeString, "Tom &amp; Ann"},
	} {
		if got := Fill(c.in, vars, c.escape); got != c.want {
			t.Errorf("Fill(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Fill("{name}", nil, nil); got != "{name}" {
		t.Errorf("no variables: %q", got)
	}
}
