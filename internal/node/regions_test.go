package node

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func checkOf(t *testing.T, id string) regionCheck {
	t.Helper()
	for _, c := range regionChecks {
		if c.id == id {
			return c
		}
	}
	t.Fatalf("no check %s", id)
	return regionCheck{}
}

// Each site's answer, as the sites gave it from a server in the Netherlands.
func TestRegionReplies(t *testing.T) {
	at := func(raw string) *url.URL { u, _ := url.Parse(raw); return u }
	for id, c := range map[string]struct {
		r    regionReply
		want string
	}{
		"google":      {regionReply{status: 200, body: `x,"MgUcDb":"NL",y`}, "NL"},
		"youtube":     {regionReply{status: 200, body: `"INNERTUBE":{"countryCode":"NL"}`}, "NL"},
		"netflix":     {regionReply{status: 200, final: at("https://www.netflix.com/nl-en/")}, "NL"},
		"chatgpt":     {regionReply{status: 200, body: "fl=1\nip=203.0.113.7\nloc=NL\ntls=TLSv1.3\n"}, "NL"},
		"prime_video": {regionReply{status: 200, body: `{"currentTerritory":"NL"}`}, "NL"},
		"deezer":      {regionReply{status: 200, body: `{"country_iso":"NL","open":true}`}, "NL"},
		"apple":       {regionReply{status: 200, body: "NL\n"}, "NL"},
		"steam":       {regionReply{status: 200, body: `data-config="{&quot;x&quot;}" "countrycode":"NL"`}, "NL"},
		"playstation": {regionReply{status: 200, final: at("https://www.playstation.com/en-gb/")}, "GB"},
		"tiktok":      {regionReply{status: 200, body: `"region":"NL","x":1`}, "NL"},
		"ookla":       {regionReply{status: 200, body: `{"location":{"countryCode":"NL","cityName":"Amsterdam"}}`}, "NL"},
		"jetbrains":   {regionReply{status: 200, body: `{"name":"The Netherlands","code":"NL"}`}, "NL"},
		"bing":        {regionReply{status: 200, body: `_G={Region:"NL",Lang:"en"}`}, "NL"},
	} {
		if got := checkOf(t, id).country(c.r); got != c.want {
			t.Errorf("%s: %q, want %q", id, got, c.want)
		}
	}
	if got := checkOf(t, "netflix").country(regionReply{status: 200, final: at("https://www.netflix.com/")}); got != "" {
		t.Errorf("netflix without a region in the path: %q", got)
	}

	for name, c := range map[string]struct {
		id          string
		r           regionReply
		open, known bool
	}{
		"search":           {"google_search", regionReply{status: 200, final: at("https://www.google.com/search?q=mikan")}, true, true},
		"search captcha":   {"google_search", regionReply{status: 200, final: at("https://www.google.com/sorry/index?continue=x")}, false, true},
		"search 429":       {"google_search", regionReply{status: 429}, false, true},
		"premium":          {"youtube_premium", regionReply{status: 200, body: "<html>YouTube Premium</html>"}, true, true},
		"no premium":       {"youtube_premium", regionReply{status: 200, body: "Premium is not available in your country"}, false, true},
		"no music":         {"youtube_music", regionReply{status: 200, body: "YouTube Music is not available in your area"}, false, true},
		"reddit":           {"reddit", regionReply{status: 200}, true, true},
		"reddit blocked":   {"reddit", regionReply{status: 403}, false, true},
		"reddit something": {"reddit", regionReply{status: 502}, false, false},
	} {
		open, known := checkOf(t, c.id).access(c.r)
		if open != c.open || known != c.known {
			t.Errorf("%s: open %v known %v", name, open, known)
		}
	}
}

// Every site is asked over each family the node has an address on; a family without one
// is no_ip, a site that does not answer is unreachable, an answer that cannot be read unknown.
func TestServiceRegions(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/trace":
			_, _ = w.Write([]byte("ip=203.0.113.7\nloc=NL\n"))
		case "/google":
			_, _ = w.Write([]byte(`"MgUcDb":"DE"`))
		case "/odd":
			_, _ = w.Write([]byte("nothing here"))
		case "/reddit":
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	down := httptest.NewTLSServer(http.NotFoundHandler())
	gone := down.URL
	down.Close()
	checks := []regionCheck{
		{id: "google", url: srv.URL + "/google", country: checkOf(t, "google").country},
		{id: "odd", url: srv.URL + "/odd", country: checkOf(t, "google").country},
		{id: "gone", url: gone + "/x", country: checkOf(t, "google").country},
		{id: "reddit", url: srv.URL + "/reddit", access: checkOf(t, "reddit").access},
	}
	noV6 := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network is unreachable") })}
	res, err := serviceRegions(context.Background(), checks, map[string]*http.Client{"4": srv.Client(), "6": noV6}, srv.URL+"/trace")
	if err != nil {
		t.Fatal(err)
	}
	if res.IPv4 != "203.0.113.7" || res.IPv6 != "" || len(res.Services) != 4 {
		t.Fatalf("%+v", res)
	}
	got := map[string]string{}
	for _, s := range res.Services {
		v := s.V4.Country + s.V4.Error
		if s.V4.Open != nil {
			v = map[bool]string{true: "open", false: "closed"}[*s.V4.Open]
		}
		got[s.Service] = v
		if s.V6.Error != "no_ip" {
			t.Errorf("%s over IPv6 without an address: %+v", s.Service, s.V6)
		}
	}
	want := map[string]string{"google": "DE", "odd": "unknown", "gone": "unreachable", "reddit": "closed"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if !res.Services[3].Access || res.Services[0].Access {
		t.Error("access marks the checks that answer yes or no")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
