//go:build e2e

// Node end-to-end tests: real mihomo clients talk to mikan-node through every preset.
// Run with test/e2e/run.sh.
package e2e

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"mikan/internal/nodeapi"
)

const (
	clientA = "client-a"
	clientB = "client-b"
	mib     = 1 << 20
)

var protos = []string{"vision", "xhttp", "hy2", "tuic"}

func socksPort(slot int, proto string) int {
	for i, p := range protos {
		if p == proto {
			return 10000 + slot*10 + i + 1
		}
	}
	panic(proto)
}

func udpPort(slot int, proto string) int {
	if proto == "hy2" {
		return 20000 + slot*10 + 3
	}
	return 20000 + slot*10 + 4
}

func slotName(n int) string { return fmt.Sprintf("s%06d", n) }

// ---------- node API over the unix socket ----------

type nodeClient struct{ hc *http.Client }

func newNode() *nodeClient {
	sock := "/run/mikan/node.sock"
	return &nodeClient{hc: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}}
}

func (n *nodeClient) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, "http://node"+path, body)
	resp, err := n.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, msg)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// drain collects counters until two consecutive empty batches, acking each one.
func (n *nodeClient) drain(t *testing.T) map[string]nodeapi.Traffic {
	t.Helper()
	total := map[string]nodeapi.Traffic{}
	for quiet := 0; quiet < 2; {
		var c nodeapi.Counters
		if err := n.call(http.MethodGet, "/v1/counters", nil, &c); err != nil {
			t.Fatal(err)
		}
		for k, v := range c.Slots {
			cur := total[k]
			total[k] = nodeapi.Traffic{Up: cur.Up + v.Up, Down: cur.Down + v.Down}
		}
		if err := n.call(http.MethodPost, "/v1/counters/ack", nodeapi.AckRequest{Epoch: c.Epoch, Seq: c.Seq}, nil); err != nil {
			t.Fatal(err)
		}
		if len(c.Slots) == 0 {
			quiet++
		} else {
			quiet = 0
		}
		time.Sleep(400 * time.Millisecond)
	}
	return total
}

var state nodeapi.DesiredState

func policies(overrides map[int]nodeapi.Policy) []nodeapi.Policy {
	var out []nodeapi.Policy
	for i := 1; i <= len(state.Slots); i++ {
		p := nodeapi.Policy{Slot: slotName(i), Allowed: i != 4, QuotaRemaining: -1}
		if o, ok := overrides[i]; ok {
			p = o
			p.Slot = slotName(i)
		}
		out = append(out, p)
	}
	return out
}

var defaultOverrides = map[int]nodeapi.Policy{
	3: {Allowed: true, DeviceLimit: 1, QuotaRemaining: -1},
	5: {Allowed: true, QuotaRemaining: 10 * mib},
}

func TestMain(m *testing.M) {
	raw, err := os.ReadFile("/work/state.json")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	state.Policies = policies(defaultOverrides)
	var res nodeapi.ApplyResult
	if err := newNode().call(http.MethodPut, "/v1/state", state, &res); err != nil {
		fmt.Println("apply:", err)
		os.Exit(1)
	}
	for _, l := range res.Listeners {
		if !l.OK {
			fmt.Printf("listener %s failed: %s\n", l.Name, l.Error)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// ---------- traffic helpers ----------

func dial(host string, port int) (net.Conn, error) {
	d, err := proxy.SOCKS5("tcp", net.JoinHostPort(host, strconv.Itoa(port)), nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	return d.Dial("tcp", "target:9000")
}

func request(c net.Conn, op byte, n int64) error {
	hdr := make([]byte, 9)
	hdr[0] = op
	binary.BigEndian.PutUint64(hdr[1:], uint64(n))
	_, err := c.Write(hdr)
	return err
}

func download(host string, port int, n int64) (int64, error) {
	c, err := dial(host, port)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	if err := request(c, 'D', n); err != nil {
		return 0, err
	}
	got, err := io.Copy(io.Discard, c)
	if err == nil && got != n {
		err = fmt.Errorf("short download: %d of %d", got, n)
	}
	return got, err
}

func upload(host string, port int, n int64) error {
	c, err := dial(host, port)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	if err := request(c, 'U', n); err != nil {
		return err
	}
	if _, err := io.CopyN(c, zeros{}, n); err != nil {
		return err
	}
	ack := make([]byte, 1)
	if _, err := io.ReadFull(c, ack); err != nil || ack[0] != 'K' {
		return fmt.Errorf("upload ack: %v", err)
	}
	return nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// stream keeps a slow download running and records progress until stopped or broken.
type stream struct {
	bytes  atomic.Int64
	broken atomic.Int64 // unix nanos when the stream failed, 0 while alive
	conn   net.Conn
}

func startStream(t *testing.T, host string, port int) *stream {
	t.Helper()
	c, err := dial(host, port)
	if err != nil {
		t.Fatalf("stream dial %s:%d: %v", host, port, err)
	}
	if err := request(c, 'S', 256<<10); err != nil {
		t.Fatal(err)
	}
	s := &stream{conn: c}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := c.Read(buf)
			s.bytes.Add(int64(n))
			if err != nil {
				s.broken.CompareAndSwap(0, time.Now().UnixNano())
				return
			}
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for s.bytes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if s.bytes.Load() == 0 {
		t.Fatalf("stream %s:%d delivered nothing", host, port)
	}
	return s
}

// aliveFor asserts the stream keeps receiving data for d.
func (s *stream) aliveFor(t *testing.T, d time.Duration, what string) {
	t.Helper()
	before := s.bytes.Load()
	time.Sleep(d)
	if s.broken.Load() != 0 {
		t.Fatalf("%s: stream broke", what)
	}
	if s.bytes.Load() <= before {
		t.Fatalf("%s: stream stalled", what)
	}
}

func within(got, want int64, tolerance float64) bool {
	diff := float64(got - want)
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance*float64(want)
}

// ---------- tests ----------

// AC-3: учёт по юзеру совпадает с эталоном для каждого пресета.
func TestAccuracyTCP(t *testing.T) {
	n := newNode()
	for _, proto := range protos {
		t.Run(proto, func(t *testing.T) {
			n.drain(t)
			port := socksPort(1, proto)
			const big, small, smalls, up = 32 * mib, 64 << 10, 20, 16 * mib
			if _, err := download(clientA, port, big); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < smalls; i++ {
				if _, err := download(clientA, port, small); err != nil {
					t.Fatal(err)
				}
			}
			if err := upload(clientA, port, up); err != nil {
				t.Fatal(err)
			}
			got := n.drain(t)[slotName(1)]
			wantDown := int64(big + small*smalls + 1)
			wantUp := int64(9*(smalls+2) + up)
			t.Logf("%s: down %d / %d, up %d / %d", proto, got.Down, wantDown, got.Up, wantUp)
			if !within(got.Down, wantDown, 0.001) || !within(got.Up, wantUp, 0.001) {
				t.Fatalf("counted down=%d up=%d, want %d/%d (±0.1%%)", got.Down, got.Up, wantDown, wantUp)
			}
		})
	}
}

func TestAccuracyUDP(t *testing.T) {
	n := newNode()
	for _, proto := range []string{"hy2", "tuic"} {
		t.Run(proto, func(t *testing.T) {
			n.drain(t)
			conn, err := net.Dial("udp", net.JoinHostPort(clientA, strconv.Itoa(udpPort(1, proto))))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			const count, size = 2000, 500
			var recv atomic.Int64
			go func() {
				buf := make([]byte, 2048)
				for {
					_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
					k, err := conn.Read(buf)
					if err != nil {
						return
					}
					recv.Add(int64(k))
				}
			}()
			payload := bytes.Repeat([]byte{'u'}, size)
			for i := 0; i < count; i++ {
				if _, err := conn.Write(payload); err != nil {
					t.Fatal(err)
				}
				if i%50 == 49 {
					time.Sleep(10 * time.Millisecond)
				}
			}
			time.Sleep(3 * time.Second)
			got := n.drain(t)[slotName(1)]
			sent := int64(count * size)
			t.Logf("%s: sent %d, echoed %d, counted up %d down %d", proto, sent, recv.Load(), got.Up, got.Down)
			if recv.Load() < sent*95/100 {
				t.Fatalf("too much UDP loss on the test path: %d of %d", recv.Load(), sent)
			}
			if !within(got.Up, sent, 0.05) || !within(got.Down, recv.Load(), 0.05) {
				t.Fatalf("counted up=%d down=%d, sent %d echoed %d", got.Up, got.Down, sent, recv.Load())
			}
		})
	}
}

// AC-5: отключённый юзер отрезается, соседний продолжает качать.
func TestDisableCutsOnlyThatUser(t *testing.T) {
	n := newNode()
	victim := startStream(t, clientA, socksPort(2, "vision"))
	bystander := startStream(t, clientA, socksPort(6, "hy2"))
	defer victim.conn.Close()
	defer bystander.conn.Close()

	start := time.Now()
	if err := n.call(http.MethodPut, "/v1/policies", nodeapi.PoliciesRequest{Epoch: "", Policies: policies(map[int]nodeapi.Policy{
		2: {Allowed: false}, 3: defaultOverrides[3], 5: defaultOverrides[5],
	})}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for victim.broken.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if victim.broken.Load() == 0 {
		t.Fatal("disabled user's stream still alive after 3s")
	}
	t.Logf("client saw the cut after %v", time.Duration(victim.broken.Load()-start.UnixNano()))
	bystander.aliveFor(t, 3*time.Second, "bystander hy2")
	if _, err := download(clientA, socksPort(2, "vision"), 1024); err == nil {
		t.Fatal("disabled user could still download")
	}
	if err := n.call(http.MethodPut, "/v1/policies", nodeapi.PoliciesRequest{Policies: policies(defaultOverrides)}, nil); err != nil {
		t.Fatal(err)
	}
}

// AC-4: второе устройство при лимите 1 отклоняется, после окна освобождения проходит.
func TestDeviceLimit(t *testing.T) {
	first := startStream(t, clientA, socksPort(3, "vision"))
	if _, err := download(clientB, socksPort(3, "vision"), 1024); err == nil {
		t.Fatal("second device admitted over the limit")
	}
	first.aliveFor(t, time.Second, "first device")
	first.conn.Close()
	time.Sleep(5 * time.Second) // MIKAN_DEVICE_RELEASE=3s in compose
	if _, err := download(clientB, socksPort(3, "vision"), 1024); err != nil {
		t.Fatalf("second device after release: %v", err)
	}
}

// AC-6: новый юзер (свободный слот) подключается без пересоздания листенеров, QUIC-сессии соседей живы.
func TestNewUserKeepsQUICSessions(t *testing.T) {
	n := newNode()
	bystander := startStream(t, clientA, socksPort(6, "hy2"))
	defer bystander.conn.Close()
	st := state
	st.Revision++
	st.Policies = policies(map[int]nodeapi.Policy{3: defaultOverrides[3], 5: defaultOverrides[5], 4: {Allowed: true, QuotaRemaining: -1}})
	var res nodeapi.ApplyResult
	if err := n.call(http.MethodPut, "/v1/state", st, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Recreated) != 0 {
		t.Fatalf("listeners recreated for a policy-only change: %v", res.Recreated)
	}
	start := time.Now()
	if _, err := download(clientA, socksPort(4, "hy2"), mib); err != nil {
		t.Fatalf("new user: %v", err)
	}
	t.Logf("new user connected in %v", time.Since(start))
	bystander.aliveFor(t, 2*time.Second, "bystander hy2")
}

// AC-7: квота режет соединение не позже чем через 1 МиБ после исчерпания.
func TestQuotaCutsAtLimit(t *testing.T) {
	n := newNode()
	n.drain(t)
	got, err := download(clientA, socksPort(5, "vision"), 20*mib)
	if err == nil {
		t.Fatal("download over quota completed")
	}
	time.Sleep(500 * time.Millisecond)
	counted := n.drain(t)[slotName(5)]
	total := counted.Up + counted.Down
	t.Logf("client got %d bytes before cut, node counted %d", got, total)
	if got < 9*mib || total < 10*mib || total > 11*mib {
		t.Fatalf("client got %d, node counted %d; want the cut right after 10 MiB (≤ 11 MiB)", got, total)
	}
	if _, err := download(clientA, socksPort(5, "vision"), 1024); err == nil {
		t.Fatal("exhausted user could still download")
	}
	var h nodeapi.Health
	if err := n.call(http.MethodGet, "/v1/health", nil, &h); err != nil {
		t.Fatal(err)
	}
	if h.Core == "" || len(h.Listeners) != len(state.Inbounds) {
		t.Fatalf("health: %+v", h)
	}
}
