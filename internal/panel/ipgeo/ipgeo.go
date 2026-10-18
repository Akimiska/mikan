// Package ipgeo asks public geo databases which country an address is in. Sites and apps
// pick a client's region by such databases, and they disagree: a node bought "in Germany"
// may be Russian in one base and American in another, and then a service shows the wrong
// region or refuses. Each base answers by the address alone, with no key; the address goes
// to every one of them.
package ipgeo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// fresh is how long an answer is kept: a page reloaded or a button pressed twice do
	// not ask every base again.
	fresh = 10 * time.Minute
	// wait bounds one base; a slow one is a timeout, not a slow check.
	wait = 8 * time.Second
	// maxBody is more than any base answers about one address.
	maxBody = 64 << 10
)

// A service is a base: where to ask (%s is the address) and where the ISO country code
// is in its JSON.
type service struct {
	Name string
	URL  string
	Path []string
}

// Services are the bases asked, by the name they are known by.
var Services = []service{
	{"MaxMind", "https://api.country.is/%s", []string{"country"}},
	{"ipinfo.io", "https://ipinfo.io/%s/json", []string{"country"}},
	{"ipwho.is", "https://ipwho.is/%s", []string{"country_code"}},
	{"ipapi.co", "https://ipapi.co/%s/json/", []string{"country_code"}},
	{"ipapi.is", "https://api.ipapi.is/?q=%s", []string{"location", "country_code"}},
	{"IP2Location", "https://api.ip2location.io/?ip=%s", []string{"country_code"}},
	{"DB-IP", "https://api.db-ip.com/v2/free/%s", []string{"countryCode"}},
	{"GeoJS", "https://get.geojs.io/v1/ip/country/%s.json", []string{"country"}},
	{"FreeIPAPI", "https://freeipapi.com/api/json/%s", []string{"countryCode"}},
	{"iplocation.net", "https://api.iplocation.net/?ip=%s", []string{"country_code2"}},
}

// What a base did instead of answering.
const (
	ErrTimeout = "timeout"
	ErrLimited = "limited" // 429: too many questions from this server
	ErrFailed  = "failed"  // another status, no country in the answer
)

// GeoAnswer is one base's word.
type GeoAnswer struct {
	Service string `json:"service"`
	Country string `json:"country,omitempty" doc:"ISO 3166-1 alpha-2"`
	Error   string `json:"error,omitempty" enum:"timeout,limited,failed"`
}

// GeoCheck is what the bases say about an address.
type GeoCheck struct {
	IP      string      `json:"ip,omitempty"`
	At      time.Time   `json:"at"`
	Cached  bool        `json:"cached"`
	Answers []GeoAnswer `json:"answers"`
	// Country is what most bases say; "" when none answered.
	Country string `json:"country,omitempty" doc:"Что говорит большинство баз"`
	// Agree: every base that answered says Country.
	Agree bool `json:"agree" doc:"Все ответившие базы согласны"`
}

// ErrHost is an address no base knows: private, loopback and the like.
var ErrHost = errors.New("ipgeo: not a public address")

// Client asks the bases and keeps their answers for a while.
type Client struct {
	hc       *http.Client
	services []service
	now      func() time.Time

	mu      sync.Mutex
	results map[netip.Addr]GeoCheck
}

// New asks the bases in Services; hc nil is a client of its own.
func New(hc *http.Client, now func() time.Time) *Client {
	return newClient(hc, Services, now)
}

func newClient(hc *http.Client, services []service, now func() time.Time) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: wait}
	}
	return &Client{hc: hc, services: services, now: now, results: map[netip.Addr]GeoCheck{}}
}

// Public says whether a may be asked about.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	return a.Zone() == "" && a.IsGlobalUnicast() && !a.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

// Lookup asks every base about a at once.
func (c *Client) Lookup(ctx context.Context, a netip.Addr) (GeoCheck, error) {
	a = a.Unmap()
	if !Public(a) {
		return GeoCheck{}, ErrHost
	}
	c.mu.Lock()
	if r, ok := c.results[a]; ok && c.now().Sub(r.At) < fresh {
		c.mu.Unlock()
		r.Cached = true
		return r, nil
	}
	c.mu.Unlock()
	answers := make([]GeoAnswer, len(c.services))
	var wg sync.WaitGroup
	for i, s := range c.services {
		wg.Go(func() { answers[i] = c.ask(ctx, s, a) })
	}
	wg.Wait()
	r := GeoCheck{IP: a.String(), At: c.now(), Answers: answers}
	r.Country, r.Agree = verdict(answers)
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	c.mu.Lock()
	for k, v := range c.results {
		if c.now().Sub(v.At) >= fresh {
			delete(c.results, k)
		}
	}
	c.results[a] = r
	c.mu.Unlock()
	return r, nil
}

func (c *Client) ask(ctx context.Context, s service, a netip.Addr) GeoAnswer {
	ans := GeoAnswer{Service: s.Name}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.Replace(s.URL, "%s", a.String(), 1), nil)
	if err != nil {
		ans.Error = ErrFailed
		return ans
	}
	req.Header.Set("Accept", "application/json")
	// Some bases refuse a request without a name.
	req.Header.Set("User-Agent", "mikan-panel")
	resp, err := c.hc.Do(req)
	if err != nil {
		ans.Error = ErrFailed
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			ans.Error = ErrTimeout
		}
		return ans
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		ans.Error = ErrLimited
		return ans
	case resp.StatusCode != http.StatusOK:
		ans.Error = ErrFailed
		return ans
	}
	var body any
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		ans.Error = ErrFailed
		return ans
	}
	if code, ok := country(body, s.Path); ok {
		ans.Country = code
	} else {
		ans.Error = ErrFailed
	}
	return ans
}

// country is the ISO code at path in a JSON answer.
func country(v any, path []string) (string, bool) {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return "", false
		}
		v = m[k]
	}
	s, ok := v.(string)
	s = strings.ToUpper(strings.TrimSpace(s))
	if !ok || len(s) != 2 || s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' {
		return "", false
	}
	return s, true
}

// verdict is the country most bases name (ties to the first base's), and whether all that
// answered agree.
func verdict(answers []GeoAnswer) (string, bool) {
	count := map[string]int{}
	var order []string
	for _, a := range answers {
		if a.Country == "" {
			continue
		}
		if count[a.Country] == 0 {
			order = append(order, a.Country)
		}
		count[a.Country]++
	}
	if len(order) == 0 {
		return "", false
	}
	best := slices.MaxFunc(order, func(x, y string) int { return count[x] - count[y] })
	return best, len(order) == 1
}
