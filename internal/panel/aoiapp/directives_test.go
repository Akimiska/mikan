package aoiapp

import "testing"

func TestDirectiveCheck(t *testing.T) {
	const app = "0f8fad5b-d9cb-469f-a165-70867728950e"
	cases := []struct {
		name string
		n    NewDirective
		code string
	}{
		{"rewrite", NewDirective{Kind: KindRewriteURL, Target: "all", Params: DirectiveParams{From: "https://sub.aoimusic.ru/sub/", To: "https://sub.aoicompany.com/sub/"}}, ""},
		{"rewrite same", NewDirective{Kind: KindRewriteURL, Target: "all", Params: DirectiveParams{From: "https://a/", To: "https://a/"}}, "directive_bad_rewrite"},
		{"rewrite not a link", NewDirective{Kind: KindRewriteURL, Target: "all", Params: DirectiveParams{From: "sub.aoimusic.ru", To: "https://b/"}}, "directive_bad_rewrite"},
		{"add", NewDirective{Kind: KindAddProfile, Target: "app", AppID: app, Params: DirectiveParams{URL: "https://sub.aoimusic.ru/sub/abcdef12"}}, ""},
		{"add no url", NewDirective{Kind: KindAddProfile, Target: "all"}, "directive_bad_url"},
		{"remove no tokens", NewDirective{Kind: KindRemoveProfile, Target: "all", Params: DirectiveParams{Tokens: []string{"!"}}}, "directive_no_subscription"},
		{"remove", NewDirective{Kind: KindRemoveProfile, Target: "user", UserID: 3, Params: DirectiveParams{Tokens: []string{"https://sub.aoimusic.ru/sub/abcdef12.yaml"}}}, ""},
		{"refresh all", NewDirective{Kind: KindRefresh, Target: "all"}, ""},
		{"message empty", NewDirective{Kind: KindMessage, Target: "all", Params: DirectiveParams{Text: "  "}}, "directive_empty_text"},
		{"user without id", NewDirective{Kind: KindRefresh, Target: "user"}, "directive_no_subscription"},
		{"bad app", NewDirective{Kind: KindRefresh, Target: "app", AppID: "x"}, "directive_bad_app_id"},
		{"bad kind", NewDirective{Kind: "wipe", Target: "all"}, "directive_bad_kind"},
		{"bad target", NewDirective{Kind: KindRefresh, Target: "some"}, "directive_bad_target"},
	}
	for _, c := range cases {
		err := c.n.check()
		got := ""
		if de, ok := err.(*DirectiveError); ok {
			got = de.Code
		} else if err != nil {
			got = err.Error()
		}
		if got != c.code {
			t.Errorf("%s: got %q, want %q", c.name, got, c.code)
		}
	}
	n := NewDirective{Kind: KindRemoveProfile, Target: "user", UserID: 3, AppID: app, Params: DirectiveParams{Tokens: []string{"https://sub.aoimusic.ru/sub/abcdef12.yaml", "abcdef12"}, URL: "https://x/"}}
	if err := n.check(); err != nil {
		t.Fatal(err)
	}
	if len(n.Params.Tokens) != 1 || n.Params.Tokens[0] != "abcdef12" || n.Params.URL != "" || n.AppID != "" {
		t.Errorf("not normalized: %+v", n)
	}
}
