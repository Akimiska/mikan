package frontpage

import "testing"

// A page's file is named from Templates, never from the name asked for: a name that is none
// of them (a path, a traversal, another case) has no file and no page.
func TestFile(t *testing.T) {
	for _, name := range Templates {
		if got := File(name); got != name+".html" || Page(name) == nil {
			t.Errorf("%s: file %q, page %v", name, got, Page(name) != nil)
		}
	}
	for _, name := range []string{"", "../cats", "cats/../maintenance", "/etc/passwd", `..\cats`, "cats.html", "Cats", "cats\x00"} {
		if got := File(name); got != "" || Page(name) != nil {
			t.Errorf("%q: file %q, page %v", name, got, Page(name) != nil)
		}
	}
}
