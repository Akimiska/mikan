package reach

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fake is Globalping as its API page describes it: three probes, a home network and a
// data centre in Russia and one in Germany. Port 22 opens everywhere, 443 only from
// Germany (a block on the way), 8443 nowhere; the first poll finds nothing done yet.
type fake struct {
	mu      sync.Mutex
	started map[string]int // measurement id → port
	polls   map[string]int
	bodies  []map[string]any
	limited bool
}

var probes = []map[string]any{
	{"country": "RU", "city": "Novosibirsk", "asn": 8359, "network": "MTS", "tags": []string{"eyeball-network"}},
	{"country": "DE", "city": "Falkenstein", "asn": 24940, "network": "Hetzner Online", "tags": []string{"datacenter-network"}},
	{"country": "RU", "city": "Moscow", "asn": 50340, "network": "Selectel", "tags": []string{"datacenter-network"}},
}

func (f *fake) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/measurements", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.limited {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_exceeded","message":"API rate limit exceeded."}}`))
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.bodies = append(f.bodies, b)
		port := int(b["measurementOptions"].(map[string]any)["port"].(float64))
		id := "m" + string(rune('a'+len(f.started)))
		f.started[id] = port
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "probesCount": len(probes)})
	})
	mux.HandleFunc("GET /v1/measurements/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		id := r.PathValue("id")
		port, ok := f.started[id]
		f.polls[id]++
		first := f.polls[id] == 1
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		var results []map[string]any
		for _, p := range probes {
			res := map[string]any{"status": "in-progress"}
			if !first {
				rcv := 0
				switch {
				case port == 22, port == 443 && p["country"] == "DE":
					rcv = 3
				}
				stats := map[string]any{"total": 3, "rcv": rcv, "avg": nil}
				if rcv > 0 {
					stats["avg"] = 61.4
				}
				res = map[string]any{"status": "finished", "stats": stats}
			}
			results = append(results, map[string]any{"probe": p, "result": res})
		}
		status := "finished"
		if first {
			status = "in-progress"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "status": status, "results": results})
	})
	return mux
}

func newFake(t *testing.T) (*fake, *Client, *time.Time) {
	f := &fake{started: map[string]int{}, polls: map[string]int{}}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	now := time.Unix(1_800_000_000, 0)
	c := New(srv.URL, srv.Client(), func() time.Time { return now })
	c.pollEvery = 5 * time.Millisecond
	return f, c, &now
}

func TestCheckTCP(t *testing.T) {
	f, c, now := newFake(t)
	ctx := context.Background()
	r, err := c.CheckTCP(ctx, "203.0.113.5", []int{443, 8443})
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != VerdictBlocked || len(r.Probes) != 3 || r.Cached {
		t.Fatalf("blocked: %+v", r)
	}
	// Home networks first, then data centres, then the control.
	if r.Probes[0].Network != "MTS" || !r.Probes[0].Home || r.Probes[1].Network != "Selectel" || !r.Probes[2].Control {
		t.Fatalf("order: %+v", r.Probes)
	}
	de := r.Probes[2].Ports
	if !de[0].OK || de[0].Ms != 61 || de[0].Got != 3 || de[1].OK || r.Probes[0].Ports[0].OK {
		t.Fatalf("ports: %+v / %+v", de, r.Probes[0].Ports)
	}
	// The second port asks the probes the first one picked.
	if len(f.bodies) != 2 || f.bodies[1]["locations"] != "ma" || f.bodies[0]["target"] != "203.0.113.5" {
		t.Fatalf("requests: %+v", f.bodies)
	}
	if opts := f.bodies[0]["measurementOptions"].(map[string]any); opts["protocol"] != "TCP" || opts["packets"] != float64(packets) {
		t.Fatalf("options: %+v", opts)
	}

	// Kept for a while: no new measurement.
	if r, err := c.CheckTCP(ctx, "203.0.113.5", []int{443, 8443}); err != nil || !r.Cached || len(f.bodies) != 2 {
		t.Fatalf("cached: %+v %v", r, err)
	}
	*now = now.Add(resultFresh)

	if r, err := c.CheckTCP(ctx, "203.0.113.5", []int{22}); err != nil || r.Verdict != VerdictOK {
		t.Fatalf("open: %+v %v", r, err)
	}
	if r, err := c.CheckTCP(ctx, "203.0.113.5", []int{8443}); err != nil || r.Verdict != VerdictDown {
		t.Fatalf("closed: %+v %v", r, err)
	}

	f.limited = true
	if _, err := c.CheckTCP(ctx, "203.0.113.9", []int{22}); !errors.Is(err, ErrLimited) {
		t.Fatalf("limited: %v", err)
	}
}

func TestCheckTCPRefuses(t *testing.T) {
	f, c, _ := newFake(t)
	for _, tc := range []struct {
		host  string
		ports []int
	}{
		{"10.0.0.1", []int{22}}, {"127.0.0.1", []int{22}}, {"100.64.1.1", []int{22}}, {"localhost", []int{22}},
		{"fe80::1", []int{22}}, {"example.com/x", []int{22}}, {"203.0.113.5", nil}, {"203.0.113.5", []int{0}},
		{"203.0.113.5", []int{1, 2, 3, 4}}, {"*.example.com", []int{22}},
	} {
		if _, err := c.CheckTCP(context.Background(), tc.host, tc.ports); !errors.Is(err, ErrHost) {
			t.Errorf("%q %v: %v", tc.host, tc.ports, err)
		}
	}
	if len(f.bodies) != 0 {
		t.Fatal("the service was asked")
	}
}

func TestBusy(t *testing.T) {
	_, c, _ := newFake(t)
	c.run.Lock()
	defer c.run.Unlock()
	if _, err := c.CheckTCP(context.Background(), "example.com", []int{22}); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
}

func TestVerdict(t *testing.T) {
	p := func(control, ok bool) ReachProbe {
		return ReachProbe{ReachSource: ReachSource{Control: control}, Ports: []ReachPort{{OK: false}, {OK: ok}}}
	}
	for _, tc := range []struct {
		probes []ReachProbe
		want   string
	}{
		{[]ReachProbe{p(false, true), p(false, true), p(true, true)}, VerdictOK},
		{[]ReachProbe{p(false, true), p(false, false), p(true, true)}, VerdictPartial},
		{[]ReachProbe{p(false, false), p(false, false), p(true, true)}, VerdictBlocked},
		{[]ReachProbe{p(false, false), p(true, false)}, VerdictDown},
		// Russia opens, the control does not answer: Russia is what was asked.
		{[]ReachProbe{p(false, true), p(true, false)}, VerdictOK},
	} {
		if got := verdict(tc.probes); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc.probes, got, tc.want)
		}
	}
}
