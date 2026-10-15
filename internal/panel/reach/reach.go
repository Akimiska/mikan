// Package reach asks Globalping (globalping.io) whether TCP ports of a server open from
// Russia: its probes connect to a port a few times and say how many got through. Most of
// its Russian probes sit in home and mobile networks (MTS, Rostelecom and the like), where
// the blocks are; a checker in a data centre often passes what a home network does not.
// A few probes in Europe connect too: a port that opens there and not from Russia is
// blocked on the way, one that opens nowhere is closed or the server is down.
//
// It sees blocks of an address or a port (a block of TCP lets ping through, so it pings
// over TCP), not DPI that breaks a protocol after the connection, and not the whitelists
// of mobile networks.
//
// The contract (globalping.io/docs/api.globalping.io): POST /v1/measurements
// {"type":"ping","target":…,"locations":[{"magic":…,"limit":…}],"measurementOptions":
// {"protocol":"TCP","port":…,"packets":…}} answers 202 {"id":…}; "locations" set to an
// earlier measurement's id asks the same probes, in the same order. GET /v1/measurements/<id>
// answers {"status":"in-progress"|"finished", "results":[{"probe":{"country","city","asn",
// "network","tags"},"result":{"status","stats":{"total","rcv","avg"}}}]}. Without a token
// an address gets 250 probe answers an hour; 429 past that.
package reach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/textutil"
)

// Base is the real service.
const Base = "https://api.globalping.io"

const (
	// MaxPorts a check takes: each port is a measurement of every probe.
	MaxPorts = 3
	// packets per probe and port: one of three through is a block that leaks, not an open port.
	packets = 3
	// resultFresh is how long a check is kept: a page reloaded, a button pressed twice do
	// not ask the service again.
	resultFresh = 2 * time.Minute
	pollEvery   = time.Second
	// wait bounds a measurement: three TCP pings that time out take some 15 seconds.
	wait = 40 * time.Second
)

// locations: home networks in Russia first, two data centres there, one probe in each of
// three European countries for the control.
var locations = []map[string]any{
	{"magic": "RU+eyeball-network", "limit": 5},
	{"magic": "RU+datacenter-network", "limit": 2},
	{"magic": "DE", "limit": 1},
	{"magic": "NL", "limit": 1},
	{"magic": "FI", "limit": 1},
}

var (
	// ErrBusy: a check runs already; one at a time.
	ErrBusy = errors.New("reach: a check is running")
	// ErrLimited: the service's hourly limit for this address is spent.
	ErrLimited = errors.New("reach: rate limited")
	// ErrNoProbes: the service has no probe where asked.
	ErrNoProbes = errors.New("reach: no probes")
	// ErrHost: not a public address or a domain.
	ErrHost = errors.New("reach: bad host")
)

// Verdicts of a check.
const (
	VerdictOK      = "ok"      // opens from every Russian probe
	VerdictPartial = "partial" // opens from some: a block of some networks
	VerdictBlocked = "blocked" // opens in Europe and from no Russian probe
	VerdictDown    = "down"    // opens nowhere: closed, filtered by the host, or the server is down
)

// PortResult is one port as one probe saw it.
type ReachPort struct {
	Port int  `json:"port"`
	OK   bool `json:"ok"`
	// Sent and Got are the connections tried and made; more than half made is open.
	Sent int `json:"sent"`
	Got  int `json:"got"`
	Ms   int `json:"ms,omitempty"`
	// Pending: no answer from the probe in time.
	Pending bool `json:"pending,omitempty"`
}

// Probe is where a check came from.
type ReachSource struct {
	Country string `json:"country"`
	City    string `json:"city"`
	Network string `json:"network"`
	ASN     int    `json:"asn"`
	// Home: a home or mobile network, where the blocks are; otherwise a data centre.
	Home bool `json:"home"`
	// Control: a probe outside Russia.
	Control bool `json:"control"`
}

// ProbeResult is one probe's answer for every port.
type ReachProbe struct {
	ReachSource
	Ports []ReachPort `json:"ports"`
}

// Result is a check of one host.
type ReachCheck struct {
	At      time.Time `json:"at"`
	Host    string    `json:"host"`
	Ports   []int     `json:"ports"`
	Verdict string    `json:"verdict" enum:"ok,partial,blocked,down"`
	// Probes: Russia first, home networks before data centres, then the control.
	Probes []ReachProbe `json:"probes"`
	// Cached: the answer is a recent one kept, not a new check.
	Cached bool `json:"cached"`
}

// Client talks to Globalping.
type Client struct {
	base      string
	hc        *http.Client
	now       func() time.Time
	pollEvery time.Duration

	run sync.Mutex // one check at a time

	mu      sync.Mutex
	results map[string]ReachCheck
}

// New makes a client of the service at base ("" is the real one).
func New(base string, hc *http.Client, now func() time.Time) *Client {
	if base == "" {
		base = Base
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	if now == nil {
		now = time.Now
	}
	return &Client{base: strings.TrimSuffix(base, "/"), hc: hc, now: now, pollEvery: pollEvery, results: map[string]ReachCheck{}}
}

// CanonHost is a host the service may be asked about: a public address or a domain, in
// its canonical form.
func CanonHost(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		a = a.Unmap()
		if a.Zone() != "" || !a.IsGlobalUnicast() || a.IsPrivate() || isShared(a) {
			return "", false
		}
		return a.String(), true
	}
	if d, ok := nodeapi.CanonDomain(s); ok && d == strings.ToLower(s) && strings.Contains(d, ".") {
		return d, true
	}
	return "", false
}

// isShared: 100.64.0.0/10, the carrier's NAT, is not anyone's server.
func isShared(a netip.Addr) bool {
	return netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

// CheckTCP checks ports of host from Russia and from the control.
func (c *Client) CheckTCP(ctx context.Context, host string, ports []int) (ReachCheck, error) {
	host, ok := CanonHost(host)
	if !ok || len(ports) == 0 || len(ports) > MaxPorts {
		return ReachCheck{}, ErrHost
	}
	ports = slices.Clone(ports)
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return ReachCheck{}, ErrHost
		}
	}
	key := host + "|" + fmt.Sprint(ports)
	c.mu.Lock()
	if r, ok := c.results[key]; ok && c.now().Sub(r.At) < resultFresh {
		c.mu.Unlock()
		r.Cached = true
		return r, nil
	}
	c.mu.Unlock()
	if !c.run.TryLock() {
		return ReachCheck{}, ErrBusy
	}
	defer c.run.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*wait)
	defer cancel()

	// The first port picks the probes; the others ask the same ones at once.
	first, err := c.start(ctx, host, ports[0], locations)
	if err != nil {
		return ReachCheck{}, err
	}
	ids := []string{first}
	for _, p := range ports[1:] {
		id, err := c.start(ctx, host, p, first)
		if err != nil {
			return ReachCheck{}, err
		}
		ids = append(ids, id)
	}
	answers := make([][]answer, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() { answers[i] = c.collect(ctx, id) })
	}
	wg.Wait()
	if len(answers[0]) == 0 {
		return ReachCheck{}, ErrNoProbes
	}
	out := ReachCheck{At: c.now().UTC(), Host: host, Ports: ports}
	for i, a := range answers[0] {
		pr := ReachProbe{ReachSource: a.probe}
		for j, p := range ports {
			r := ReachPort{Port: p, Pending: true}
			// The same probes in the same order; a probe that went away is pending.
			if i < len(answers[j]) && answers[j][i].probe == a.probe {
				r = answers[j][i].port(p)
			}
			pr.Ports = append(pr.Ports, r)
		}
		out.Probes = append(out.Probes, pr)
	}
	slices.SortStableFunc(out.Probes, func(a, b ReachProbe) int {
		rank := func(p ReachSource) int {
			switch {
			case p.Control:
				return 2
			case p.Home:
				return 0
			}
			return 1
		}
		return rank(a.ReachSource) - rank(b.ReachSource)
	})
	out.Verdict = verdict(out.Probes)
	c.mu.Lock()
	for k, r := range c.results {
		if c.now().Sub(r.At) >= resultFresh {
			delete(c.results, k)
		}
	}
	c.results[key] = out
	c.mu.Unlock()
	return out, nil
}

// verdict: a probe reaches the server when any of the ports opens for it; Russia is
// judged against the control.
func verdict(probes []ReachProbe) string {
	var ru, ruOK, control int
	for _, p := range probes {
		opens := slices.ContainsFunc(p.Ports, func(r ReachPort) bool { return r.OK })
		switch {
		case p.Control:
			if opens {
				control++
			}
		default:
			ru++
			if opens {
				ruOK++
			}
		}
	}
	switch {
	case ru > 0 && ruOK == ru:
		return VerdictOK
	case control == 0:
		return VerdictDown
	case ruOK == 0:
		return VerdictBlocked
	}
	return VerdictPartial
}

// start asks for a TCP ping of host:port from locs (the list, or an earlier measurement's
// id) and returns the measurement's id.
func (c *Client) start(ctx context.Context, host string, port int, locs any) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"type": "ping", "target": host, "locations": locs,
		"measurementOptions": map[string]any{"protocol": "TCP", "port": port, "packets": packets},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/measurements", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ID    string `json:"id"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", ErrLimited
	case resp.StatusCode == http.StatusUnprocessableEntity && out.Error.Type == "no_probes_found":
		return "", ErrNoProbes
	case resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("globalping: status %d: %s", resp.StatusCode, textutil.Clip(out.Error.Message, 200))
	case !validID(out.ID):
		return "", errors.New("globalping: no measurement id")
	}
	return out.ID, nil
}

// validID: the id goes into a URL path.
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// answer is one probe's result of one measurement.
type answer struct {
	probe    ReachSource
	finished bool
	total    int
	got      int
	avg      float64
}

func (a answer) port(p int) ReachPort {
	if !a.finished {
		return ReachPort{Port: p, Pending: true}
	}
	r := ReachPort{Port: p, Sent: a.total, Got: a.got, OK: a.got*2 > a.total}
	if a.got > 0 {
		r.Ms = int(math.Round(a.avg))
	}
	return r
}

// collect polls a measurement until it is done or the wait is over, and returns what the
// probes said by then; nothing when the service never answered.
func (c *Client) collect(ctx context.Context, id string) []answer {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	tick := time.NewTicker(c.pollEvery)
	defer tick.Stop()
	var last []answer
	for {
		select {
		case <-ctx.Done():
			return last
		case <-tick.C:
		}
		res, done, err := c.get(ctx, id)
		if err != nil {
			continue
		}
		last = res
		if done {
			return last
		}
	}
}

func (c *Client) get(ctx context.Context, id string) ([]answer, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/measurements/"+id, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("globalping: status %d", resp.StatusCode)
	}
	var m struct {
		Status  string `json:"status"`
		Results []struct {
			Probe struct {
				Country string   `json:"country"`
				City    string   `json:"city"`
				ASN     int      `json:"asn"`
				Network string   `json:"network"`
				Tags    []string `json:"tags"`
			} `json:"probe"`
			Result struct {
				Status string `json:"status"`
				Stats  struct {
					Total int      `json:"total"`
					Rcv   int      `json:"rcv"`
					Avg   *float64 `json:"avg"`
				} `json:"stats"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, false, err
	}
	out := make([]answer, 0, len(m.Results))
	for _, r := range m.Results {
		country := strings.ToUpper(textutil.Clip(r.Probe.Country, 2))
		a := answer{probe: ReachSource{
			Country: country,
			City:    textutil.Clip(r.Probe.City, 64),
			Network: textutil.Clip(r.Probe.Network, 64),
			ASN:     r.Probe.ASN,
			Home:    slices.Contains(r.Probe.Tags, "eyeball-network"),
			Control: country != "RU",
		}}
		// "failed" and "offline" are done but say nothing of the port: the probe could not
		// run it, and it shows as pending.
		if r.Result.Status != "" && r.Result.Status != "in-progress" {
			a.finished = r.Result.Status == "finished"
			a.total, a.got = r.Result.Stats.Total, r.Result.Stats.Rcv
			if r.Result.Stats.Avg != nil {
				a.avg = *r.Result.Stats.Avg
			}
		}
		out = append(out, a)
	}
	return out, m.Status != "in-progress", nil
}
