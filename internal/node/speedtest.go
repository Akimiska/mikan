package node

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"mikan/internal/nodeapi"
)

// The speed test measures the node's own way to the internet, not a user's: latency and
// loss with small UDP DNS queries (ICMP needs privileges the node does not have), then
// download and upload against Cloudflare's speed test, a few seconds each.

// ErrSpeedTestBusy: a test is running already; one at a time, each moves tens of megabytes.
var ErrSpeedTestBusy = errors.New("speed test busy")

type speedTarget struct {
	DNS      string        // host:port of a DNS server answering over UDP
	Down, Up string        // URLs: GET ?bytes=N for download, POST for upload
	Pings    int           // DNS queries for latency and loss
	Spend    time.Duration // per direction
	Client   *http.Client
}

var cloudflare = speedTarget{
	DNS: "1.1.1.1:53", Down: "https://speed.cloudflare.com/__down", Up: "https://speed.cloudflare.com/__up",
	Pings: 20, Spend: 8 * time.Second,
	Client: &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true}},
}

var speedTesting sync.Mutex

// SpeedTest runs the test against Cloudflare.
func (e *Engine) SpeedTest(ctx context.Context) (nodeapi.SpeedTest, error) {
	return runSpeedTest(ctx, cloudflare, time.Now)
}

func runSpeedTest(ctx context.Context, t speedTarget, now func() time.Time) (nodeapi.SpeedTest, error) {
	if !speedTesting.TryLock() {
		return nodeapi.SpeedTest{}, ErrSpeedTestBusy
	}
	defer speedTesting.Unlock()
	res := nodeapi.SpeedTest{At: now().UTC()}
	res.PingMs, res.JitterMs, res.LossPct = pings(ctx, t.DNS, t.Pings)
	var err error
	if res.DownBps, err = download(ctx, t); err != nil {
		res.Error = "download: " + err.Error()
		return res, nil
	}
	if res.UpBps, err = upload(ctx, t); err != nil {
		res.Error = "upload: " + err.Error()
	}
	return res, nil
}

// pings sends n DNS queries one after another and returns the median round trip, the
// mean change between consecutive ones (jitter) and the share lost, in percent. With
// every query lost the times are -1.
func pings(ctx context.Context, server string, n int) (ping, jitter, loss float64) {
	var rtts []float64
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", server)
	if err == nil {
		defer conn.Close()
		buf := make([]byte, 512)
		for i := 0; i < n && ctx.Err() == nil; i++ {
			id := uint16(i + 1)
			start := time.Now()
			_ = conn.SetDeadline(start.Add(time.Second))
			if _, err := conn.Write(dnsQuery(id)); err != nil {
				continue
			}
			for {
				m, err := conn.Read(buf)
				if err != nil {
					break // timed out: lost
				}
				// A late answer to an earlier query is not this one's.
				if m >= 2 && binary.BigEndian.Uint16(buf) == id {
					rtts = append(rtts, float64(time.Since(start).Microseconds())/1000)
					break
				}
			}
			if wait := 50*time.Millisecond - time.Since(start); wait > 0 {
				time.Sleep(wait)
			}
		}
	}
	loss = 100
	if n > 0 {
		loss = round2(float64(n-len(rtts)) * 100 / float64(n))
	}
	if len(rtts) == 0 {
		return -1, -1, loss
	}
	for i := 1; i < len(rtts); i++ {
		jitter += math.Abs(rtts[i] - rtts[i-1])
	}
	if len(rtts) > 1 {
		jitter /= float64(len(rtts) - 1)
	}
	sorted := slices.Clone(rtts)
	slices.Sort(sorted)
	return round2(sorted[len(sorted)/2]), round2(jitter), loss
}

// dnsQuery asks for the A record of one.one.one.one.
func dnsQuery(id uint16) []byte {
	q := []byte{0, 0, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(q, id)
	for _, label := range []string{"one", "one", "one", "one"} {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	return append(q, 0, 0, 1, 0, 1)
}

// download reads from the test server for t.Spend and returns bits per second. The time
// counts from the first byte: the connection's setup is latency, not speed.
func download(ctx context.Context, t speedTarget) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, t.Spend+15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Down+"?bytes=1000000000", nil)
	if err != nil {
		return 0, err
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, errors.New(resp.Status)
	}
	buf := make([]byte, 64<<10)
	var total int64
	var start time.Time
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if start.IsZero() {
				start = time.Now()
			} else {
				total += int64(n)
			}
		}
		if err != nil || !start.IsZero() && time.Since(start) >= t.Spend {
			if err != nil && err != io.EOF && total == 0 {
				return 0, err
			}
			break
		}
	}
	return bps(total, time.Since(start)), nil
}

// upload sends chunks of 4 MB one after another for t.Spend and returns bits per second.
func upload(ctx context.Context, t speedTarget) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, t.Spend+15*time.Second)
	defer cancel()
	chunk := make([]byte, 4<<20)
	var total int64
	start := time.Now()
	for time.Since(start) < t.Spend {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Up, bytes.NewReader(chunk))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := t.Client.Do(req)
		if err != nil {
			if total > 0 {
				break
			}
			return 0, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return 0, errors.New(resp.Status)
		}
		total += int64(len(chunk))
	}
	return bps(total, time.Since(start)), nil
}

func bps(n int64, d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(float64(n) * 8 / d.Seconds())
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
