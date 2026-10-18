package node

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"mikan/internal/nodeapi"
)

// What the sites themselves make of the node's address: a geo base says where an IP is, a
// site picks the country it serves (and whether it lets the address in at all) by its own
// lights, from the address the request comes from. So these run on the node, over IPv4
// and IPv6 apart, each a plain GET as a browser makes it.

// regionReply is what a check reads: the status, where the redirects ended, the body.
type regionReply struct {
	status int
	final  *url.URL
	body   string
}

// regionCheck is one site: country reads the country it serves the address; access says
// whether it lets the address in (false: blocked, a captcha, "not in your country").
type regionCheck struct {
	id      string
	url     string
	country func(r regionReply) string
	access  func(r regionReply) (open, known bool)
}

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// first returns the first submatch of re in the body, when it is a country code.
func first(re *regexp.Regexp) func(r regionReply) string {
	return func(r regionReply) string {
		if m := re.FindStringSubmatch(r.body); m != nil {
			return strings.ToUpper(m[1])
		}
		return ""
	}
}

// pathCountry reads the country of a path such as /nl-en/ or /en-gb/: the part that is a
// country (part 0 or 1).
func pathCountry(part int, re *regexp.Regexp) func(r regionReply) string {
	return func(r regionReply) string {
		if r.final == nil {
			return ""
		}
		m := re.FindStringSubmatch(r.final.Path)
		if m == nil {
			return ""
		}
		return strings.ToUpper(m[1+part])
	}
}

func jsonField(path ...string) func(r regionReply) string {
	return func(r regionReply) string {
		var v any
		if json.Unmarshal([]byte(r.body), &v) != nil {
			return ""
		}
		for _, p := range path {
			m, _ := v.(map[string]any)
			v = m[p]
		}
		s, _ := v.(string)
		return strings.ToUpper(s)
	}
}

// trace reads Cloudflare's /cdn-cgi/trace: loc= is the country it gives the address.
var trace = first(regexp.MustCompile(`(?m)^loc=([A-Za-z]{2})$`))

// notIn: the site answers, and says nothing of being unavailable here.
func notIn(phrases ...string) func(r regionReply) (bool, bool) {
	return func(r regionReply) (bool, bool) {
		if r.status != http.StatusOK {
			return false, r.status == http.StatusForbidden || r.status == http.StatusUnavailableForLegalReasons
		}
		low := strings.ToLower(r.body)
		for _, p := range phrases {
			if strings.Contains(low, p) {
				return false, true
			}
		}
		return true, true
	}
}

var regionChecks = []regionCheck{
	{id: "google", url: "https://www.google.com/", country: first(regexp.MustCompile(`"MgUcDb":"([A-Z]{2})"`))},
	{id: "google_search", url: "https://www.google.com/search?q=mikan", access: func(r regionReply) (bool, bool) {
		if r.status == http.StatusTooManyRequests || (r.final != nil && strings.HasPrefix(r.final.Path, "/sorry")) {
			return false, true
		}
		return r.status == http.StatusOK, r.status == http.StatusOK
	}},
	{id: "youtube", url: "https://www.youtube.com/", country: first(regexp.MustCompile(`"countryCode":"([A-Z]{2})"`))},
	{id: "youtube_premium", url: "https://www.youtube.com/premium", access: notIn("is not available in your country")},
	{id: "youtube_music", url: "https://music.youtube.com/", access: notIn("not available in your area", "is not available in your country")},
	{id: "netflix", url: "https://www.netflix.com/", country: pathCountry(0, regexp.MustCompile(`^/([a-z]{2})(?:-[a-z]{2})?(?:/|$)`))},
	{id: "chatgpt", url: "https://chatgpt.com/cdn-cgi/trace", country: trace},
	{id: "cloudflare", url: "https://www.cloudflare.com/cdn-cgi/trace", country: trace},
	{id: "prime_video", url: "https://www.primevideo.com/", country: first(regexp.MustCompile(`"currentTerritory":"([A-Z]{2})"`))},
	{id: "deezer", url: "https://api.deezer.com/infos", country: jsonField("country_iso")},
	{id: "apple", url: "https://gspe1-ssl.ls.apple.com/pep/gcc", country: func(r regionReply) string { return strings.ToUpper(strings.TrimSpace(r.body)) }},
	{id: "steam", url: "https://store.steampowered.com/app/10", country: first(regexp.MustCompile(`"countrycode":"([A-Z]{2})"`))},
	{id: "playstation", url: "https://www.playstation.com/", country: pathCountry(1, regexp.MustCompile(`^/([a-z]{2})-([a-z]{2})(?:/|$)`))},
	{id: "tiktok", url: "https://www.tiktok.com/", country: first(regexp.MustCompile(`"region":"([A-Z]{2})"`))},
	{id: "ookla", url: "https://www.speedtest.net/api/js/config-sdk", country: jsonField("location", "countryCode")},
	{id: "jetbrains", url: "https://data.services.jetbrains.com/geo", country: jsonField("code")},
	{id: "bing", url: "https://www.bing.com/search?q=mikan", country: first(regexp.MustCompile(`Region:"([A-Z]{2})"`))},
	{id: "reddit", url: "https://www.reddit.com/", access: func(r regionReply) (bool, bool) {
		switch r.status {
		case http.StatusOK:
			return true, true
		case http.StatusForbidden:
			return false, true
		}
		return false, false
	}},
}

// regionBody is how much of a page is read: the markers sit in the first part of it.
const regionBody = 2 << 20

const regionAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

// regionClient goes out over one family only, past the node's own listeners and rules.
func regionClient(network string) *http.Client {
	d := &net.Dialer{Timeout: 8 * time.Second}
	return &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 8 * time.Second,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return d.DialContext(ctx, network, addr) }},
	}
}

// fetchRegion makes the browser's GET and reads the reply.
func fetchRegion(ctx context.Context, c *http.Client, u string) (regionReply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return regionReply{}, err
	}
	req.Header.Set("User-Agent", regionAgent)
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.Do(req)
	if err != nil {
		return regionReply{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, regionBody))
	return regionReply{status: resp.StatusCode, final: resp.Request.URL, body: string(body)}, nil
}

func (c regionCheck) run(ctx context.Context, client *http.Client) nodeapi.RegionAnswer {
	r, err := fetchRegion(ctx, client, c.url)
	if err != nil {
		return nodeapi.RegionAnswer{Error: "unreachable"}
	}
	if c.access != nil {
		open, known := c.access(r)
		if !known {
			return nodeapi.RegionAnswer{Error: "unknown"}
		}
		return nodeapi.RegionAnswer{Open: &open}
	}
	if cc := c.country(r); countryCode.MatchString(cc) {
		return nodeapi.RegionAnswer{Country: cc}
	}
	return nodeapi.RegionAnswer{Error: "unknown"}
}

// traceIP is the address Cloudflare sees: the node's own on that family.
var traceIP = regexp.MustCompile(`(?m)^ip=(\S+)$`)

var regionsRunning sync.Mutex

// ErrRegionsBusy: a check is running already.
var ErrRegionsBusy = errors.New("service regions busy")

// ServiceRegions asks every site at once, over IPv4 and IPv6; a family the node has no
// address on is not asked. About ten seconds.
func (e *Engine) ServiceRegions(ctx context.Context) (nodeapi.ServiceRegions, error) {
	if !regionsRunning.TryLock() {
		return nodeapi.ServiceRegions{}, ErrRegionsBusy
	}
	defer regionsRunning.Unlock()
	return serviceRegions(ctx, regionChecks, map[string]*http.Client{"4": regionClient("tcp4"), "6": regionClient("tcp6")}, regionTrace)
}

// regionTrace tells whether the node has an address of a family, and which.
const regionTrace = "https://www.cloudflare.com/cdn-cgi/trace"

func serviceRegions(ctx context.Context, checks []regionCheck, clients map[string]*http.Client, traceURL string) (nodeapi.ServiceRegions, error) {
	out := nodeapi.ServiceRegions{At: time.Now(), Services: make([]nodeapi.ServiceRegion, len(checks))}
	have := map[string]bool{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for fam, c := range clients {
		wg.Go(func() {
			r, err := fetchRegion(ctx, c, traceURL)
			ip := ""
			if m := traceIP.FindStringSubmatch(r.body); err == nil && m != nil {
				ip = m[1]
			}
			mu.Lock()
			defer mu.Unlock()
			have[fam] = err == nil
			if fam == "4" {
				out.IPv4 = ip
			} else {
				out.IPv6 = ip
			}
		})
	}
	wg.Wait()
	for i, chk := range checks {
		out.Services[i] = nodeapi.ServiceRegion{Service: chk.id, Access: chk.access != nil}
		for fam, c := range clients {
			dst := &out.Services[i].V4
			if fam == "6" {
				dst = &out.Services[i].V6
			}
			if !have[fam] {
				*dst = nodeapi.RegionAnswer{Error: "no_ip"}
				continue
			}
			wg.Go(func() { *dst = chk.run(ctx, c) })
		}
	}
	wg.Wait()
	return out, nil
}
