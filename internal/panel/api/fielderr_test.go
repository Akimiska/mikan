package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// direct422 are the 422s a handler builds itself, because their top-level code names the
// kind of refusal (a template, a destination, a certificate) and the details carry the
// reasons. Every other refusal of an input goes through fielderr.go.
var direct422 = []struct{ file, title, why string }{
	{"cascade.go", `"cascade"`, "the exit's own refusals; the web words the details"},
	{"certs.go", `"bad_certificate"`, "the detail is the certificate check's text, with the host"},
	{"warp.go", `"warp"`, "the details are built by warpDetail from the WARP account's errors"},
	{"inbounds.go", `"bad_name"`, "a subscription name; the detail says which rule"},
	{"inbounds.go", `"bad_"+edit.Field`, "the part of a protocol the form edits (bad_port, bad_xhttp…); the detail says why"},
	{"inbounds.go", `"bad_dest"`, "the camouflage site; the detail says why"},
	{"inbounds.go", `"invalid_config"`, "the template; the detail says why, with the line"},
	{"targets.go", `"bad_dest"`, "the camouflage site; the detail says why"},
}

// A refused input is built in fielderr.go, so that the UI reads one shape (ApiError in
// web/src/api/client.ts). A 422 built anywhere else is a second one, unless direct422 names it.
func TestNo422OutsideFieldErr(t *testing.T) {
	call := regexp.MustCompile(`huma\.Error422UnprocessableEntity\(\s*([^,)]+?)\s*[,)]`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	type site struct{ file, title string }
	seen := map[site]bool{}
	var calls int
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "fielderr.go" {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(raw), -1) {
			calls++
			s := site{file, m[1]}
			seen[s] = true
			listed := false
			for _, d := range direct422 {
				listed = listed || d.file == s.file && d.title == s.title
			}
			if !listed {
				t.Errorf("%s: huma.Error422UnprocessableEntity(%s…) builds a 422 by hand: use fieldErr, fieldErrValue, fieldErrs or refusal (fielderr.go), or name it in direct422 with the reason", file, s.title)
			}
		}
	}
	if calls == 0 {
		t.Fatal("no 422 found: the scan lost its way")
	}
	for _, d := range direct422 {
		if !seen[site{d.file, d.title}] {
			t.Errorf("direct422 lists %s %s, which nothing sends any more", d.file, d.title)
		}
	}
}
