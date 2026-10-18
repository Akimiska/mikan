package acmechallenge

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	token   = "evaGxfADs6pSRb2LAv9IZf17Dt3juxGJ-PCt92wr-oA"
	keyAuth = token + ".9jg46WB3rR_AHD-EBXdN7cBkH1WOu0tA3M9fm21mqTI"
)

func get(t *testing.T, addr, path string) (int, string) {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// The port is taken while a token is pending and given back after: a web server of the
// admin's may use it the rest of the time.
func TestPortOnlyWhilePending(t *testing.T) {
	s := New("127.0.0.1:0")
	var addr string
	s.listen = func(network, a string) (net.Listener, error) {
		ln, err := net.Listen(network, a)
		if err == nil {
			addr = ln.Addr().String()
		}
		return ln, err
	}
	if s.Listening() {
		t.Fatal("listens before any challenge")
	}
	if err := s.Present(token, keyAuth); err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, addr, Prefix+token); code != 200 || body != keyAuth {
		t.Fatalf("token: %d %q", code, body)
	}
	if code, _ := get(t, addr, Prefix+"AAAAAAAAAAAAAAAAAAAAAAAA"); code != 404 {
		t.Fatalf("another token: %d", code)
	}
	if code, _ := get(t, addr, "/"); code != 404 {
		t.Fatalf("root: %d", code)
	}
	s.CleanUp(token)
	if s.Listening() {
		t.Fatal("still listens without a challenge")
	}
	if _, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		t.Fatal("the port is still held")
	}
}

// A panel gone in the middle of a challenge does not keep the port for ever.
func TestTokensExpire(t *testing.T) {
	now := time.Now()
	s := New("127.0.0.1:0")
	s.now = func() time.Time { return now }
	if err := s.Present(token, keyAuth); err != nil {
		t.Fatal(err)
	}
	now = now.Add(TTL + time.Second)
	if s.Pending() != 0 || s.Listening() {
		t.Fatal("an expired token is still answered")
	}
}

// The timer that expires the first token arms itself again for a token still pending:
// nothing else may come to expire it.
func TestExpiryTimerRearmsForLaterTokens(t *testing.T) {
	var clock atomic.Int64
	start := time.Now()
	clock.Store(start.UnixNano())
	s := New("127.0.0.1:0")
	s.now = func() time.Time { return time.Unix(0, clock.Load()) }
	defer s.Close()
	if err := s.Present(token, keyAuth); err != nil {
		t.Fatal(err)
	}
	// The second token comes just before the first runs out: the timer is due in about a
	// second and a half, when only the first has expired.
	clock.Store(start.Add(TTL - 500*time.Millisecond).UnixNano())
	other := strings.Repeat("b", 43)
	if err := s.Present(other, other+".9jg46WB3rR_AHD-EBXdN7cBkH1WOu0tA3M9fm21mqTI"); err != nil {
		t.Fatal(err)
	}
	clock.Store(start.Add(TTL + time.Second).UnixNano())
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		_, first := s.tokens[token]
		armed := !first && s.timer != nil && s.timer.Stop()
		s.mu.Unlock()
		if armed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no timer is armed for the token left (first still there: %v)", first)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestBusyPortNamesTheHolder(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	s := New(held.Addr().String())
	s.holder = func(int) string { return "nginx" }
	err = s.Present(token, keyAuth)
	var be *BusyError
	if !errors.As(err, &be) || !errors.Is(err, ErrPortBusy) || be.Holder != "nginx" {
		t.Fatalf("busy port: %v", err)
	}
	if s.Pending() != 0 {
		t.Fatal("a token kept without a port")
	}
}

func TestRefusesWhatIsNoToken(t *testing.T) {
	s := New("127.0.0.1:0")
	for _, c := range [][2]string{
		{"short", "short.abc"},
		{token, keyAuth + "/../x"},
		{token, "other." + strings.Repeat("a", 43)},
		{token + "!", token + "!." + strings.Repeat("a", 43)},
		{strings.Repeat("a", 300), strings.Repeat("a", 300) + "." + strings.Repeat("a", 43)},
	} {
		if err := s.Present(c[0], c[1]); err == nil {
			t.Errorf("%q accepted", c[0])
		}
	}
	if s.Listening() {
		t.Fatal("listens for nothing")
	}
}

func TestParseListen(t *testing.T) {
	for in, want := range map[string]string{
		"":                ":80",
		"  ":              ":80",
		":80":             ":80",
		"127.0.0.1:18080": "127.0.0.1:18080",
		"localhost:18080": "localhost:18080",
		"[::1]:18080":     "[::1]:18080",
		"0.0.0.0:8080":    "0.0.0.0:8080",
		":080":            ":80",
	} {
		if got, err := ParseListen(in); err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"80", "127.0.0.1", ":0", ":65536", ":-1", "127.0.0.1:http", "example.com:80", "127.0.0.1:80/path", "::1:80"} {
		if got, err := ParseListen(in); err == nil {
			t.Errorf("%q: accepted as %q", in, got)
		}
	}
}

// The holder is found through the socket's inode, as on a real /proc.
func TestHolderFromProc(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	proc := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(proc, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("net/tcp", "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1 0000000000000000 100 0 0 10 0\n")
	write("731/comm", "nginx\n")
	if err := os.MkdirAll(filepath.Join(proc, "731", "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[4242]", filepath.Join(proc, "731", "fd", "6")); err != nil {
		t.Fatal(err)
	}
	if got := holderIn(proc, 80); got != "nginx" {
		t.Fatalf("holder %q", got)
	}
	if got := holderIn(proc, 443); got != "" {
		t.Fatalf("443: %q", got)
	}
}

// A web server of another user is named by what it answers with, not by its process.
func TestServerNameFromItsAnswer(t *testing.T) {
	for in, want := range map[string]string{
		"nginx/1.24.0 (Ubuntu)":  "nginx",
		"nginx":                  "nginx",
		"Caddy":                  "caddy",
		"Apache/2.4.58 (Ubuntu)": "apache",
		"":                       "",
		"<script>":               "",
	} {
		if got := serverName(in); got != want {
			t.Errorf("serverName(%q) = %q, want %q", in, got, want)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.26.3")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if got := serverOn(srv.URL + "/"); got != "nginx" {
		t.Fatalf("serverOn = %q", got)
	}
}

// Listening says whether the port is held now.
func (s *Server) Listening() bool { return s.Bound() != "" }

// A node told to redirect holds port 80 for good: a challenge is still answered, the rest
// goes to the same host and path over https, and off gives the port back.
func TestRedirect(t *testing.T) {
	s := New("127.0.0.1:0")
	var addr string
	s.listen = func(network, a string) (net.Listener, error) {
		ln, err := net.Listen(network, a)
		if err == nil {
			addr = ln.Addr().String()
		}
		return ln, err
	}
	if err := s.SetRedirect(true); err != nil {
		t.Fatal(err)
	}
	if !s.Redirecting() {
		t.Fatal("not redirecting")
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ask := func(host, path string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
		req.Host = host
		resp, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Location")
	}
	if code, to := ask("node.example.com", "/a/b?x=1"); code != http.StatusMovedPermanently || to != "https://node.example.com/a/b?x=1" {
		t.Fatalf("redirect: %d %q", code, to)
	}
	if code, to := ask("203.0.113.5:80", "/"); code != http.StatusMovedPermanently || to != "https://203.0.113.5/" {
		t.Fatalf("by address: %d %q", code, to)
	}
	if code, _ := ask("evil.com/x", "/"); code != http.StatusBadRequest {
		t.Fatalf("a host that is no host: %d", code)
	}
	if err := s.Present(token, keyAuth); err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, addr, Prefix+token); code != 200 || body != keyAuth {
		t.Fatalf("token while redirecting: %d %q", code, body)
	}
	s.CleanUp(token)
	if !s.Listening() {
		t.Fatal("the redirect lost the port with the last token")
	}
	if err := s.SetRedirect(false); err != nil {
		t.Fatal(err)
	}
	if s.Listening() || s.Redirecting() {
		t.Fatal("still holds the port")
	}
}
