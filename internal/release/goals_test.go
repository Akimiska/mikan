package release

import "testing"

// A reader shows the goals it can and leaves the rest out; an index without goals, or
// with goals it cannot read at all, still gives its releases.
func TestIndexGoalsLenient(t *testing.T) {
	ix, err := DecodeIndex([]byte(`{"schema":1,"releases":[{"version":"0.5.0.1","channel":"stable","manifest":"https://x/m.json","from":"0.4.5"}],
		"goals":{"donate":"https://web.tribute.tg/d/REA","items":[
			{"id":"ok","title":{"ru":"Цель"},"target":100,"raised":10,"currency":"USD","status":"open"},
			{"id":"ok","title":{"ru":"Дубль"},"target":100,"currency":"USD","status":"open"},
			{"id":"bad status","title":{"ru":"x"},"target":100,"currency":"USD","status":"maybe"},
			{"id":"future","title":{"ru":"x"},"target":100,"currency":"USD","status":"open","new_field":true},
			{"id":"evil","title":{"ru":"x"},"target":100,"currency":"USD","status":"open","url":"javascript:alert(1)"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Releases) != 1 {
		t.Fatalf("releases: %+v", ix.Releases)
	}
	if len(ix.Goals.Items) != 2 || ix.Goals.Items[0].ID != "ok" || ix.Goals.Items[1].ID != "future" {
		t.Fatalf("goals: %+v", ix.Goals.Items)
	}
	for _, goals := range []string{`"goals":{"donate":"javascript:x","items":[]}`, `"goals":[1,2]`, `"goals":"x"`} {
		ix, err := DecodeIndex([]byte(`{"releases":[{"version":"0.5.0.1","channel":"stable","manifest":"https://x/m.json","from":"0.4.5"}],` + goals + `}`))
		if err != nil || len(ix.Releases) != 1 || len(ix.Goals.Items) != 0 {
			t.Errorf("%s: %v %+v", goals, err, ix)
		}
	}
}
