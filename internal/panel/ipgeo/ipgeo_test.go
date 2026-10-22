package ipgeo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The bases are asked at once; most of them name the country, a base that disagrees or
// fails shows as such, and the answer is kept for a while.
func TestLookup(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if !strings.Contains(r.URL.String(), "203.0.113.7") {
			t.Errorf("asked about %s", r.URL)
		}
		base, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		switch base {
		case "a":
			_, _ = w.Write([]byte(`{"country":"de"}`))
		case "b":
			_, _ = w.Write([]byte(`{"location":{"country_code":"DE"}}`))
		case "c":
			_, _ = w.Write([]byte(`{"country_code":"RU"}`))
		case "d":
			w.WriteHeader(http.StatusTooManyRequests)
		case "e":
			_, _ = w.Write([]byte(`{"country":"Germany"}`))
		}
	}))
	defer srv.Close()
	now := time.Unix(1_800_000_000, 0)
	c := newClient(srv.Client(), []service{
		{"A", srv.URL + "/a?ip=%s", []string{"country"}},
		{"B", srv.URL + "/b?q=%s", []string{"location", "country_code"}},
		{"C", srv.URL + "/c/%s", []string{"country_code"}},
		{"D", srv.URL + "/d/%s", []string{"country"}},
		{"E", srv.URL + "/e/%s", []string{"country"}},
	}, func() time.Time { return now })
	r, err := c.Lookup(context.Background(), netip.MustParseAddr("203.0.113.7"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Country != "DE" || r.Agree {
		t.Fatalf("verdict: %+v", r)
	}
	want := []GeoAnswer{{"A", "DE", ""}, {"B", "DE", ""}, {"C", "RU", ""}, {"D", "", ErrLimited}, {"E", "", ErrFailed}}
	for i, a := range r.Answers {
		if a != want[i] {
			t.Errorf("answer %d = %+v, want %+v", i, a, want[i])
		}
	}
	if again, _ := c.Lookup(context.Background(), netip.MustParseAddr("203.0.113.7")); !again.Cached || asked.Load() != 5 {
		t.Fatalf("not kept: cached %v, asked %d", again.Cached, asked.Load())
	}
	now = now.Add(fresh)
	if again, _ := c.Lookup(context.Background(), netip.MustParseAddr("203.0.113.7")); again.Cached || asked.Load() != 10 {
		t.Fatalf("kept too long: cached %v, asked %d", again.Cached, asked.Load())
	}
	for _, a := range []string{"10.0.0.1", "127.0.0.1", "100.64.1.1", "fe80::1"} {
		if _, err := c.Lookup(context.Background(), netip.MustParseAddr(a)); err != ErrHost {
			t.Errorf("%s: %v", a, err)
		}
	}
}

func TestVerdict(t *testing.T) {
	for _, c := range []struct {
		in    []GeoAnswer
		want  string
		agree bool
	}{
		{nil, "", false},
		{[]GeoAnswer{{Error: ErrTimeout}}, "", false},
		{[]GeoAnswer{{Country: "NL"}, {Country: "NL"}}, "NL", true},
		{[]GeoAnswer{{Country: "US"}, {Country: "NL"}}, "US", false},
		{[]GeoAnswer{{Country: "US"}, {Country: "NL"}, {Country: "NL"}}, "NL", false},
	} {
		if got, agree := verdict(c.in); got != c.want || agree != c.agree {
			t.Errorf("verdict(%+v) = %q %v, want %q %v", c.in, got, agree, c.want, c.agree)
		}
	}
}
