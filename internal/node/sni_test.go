package node

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/proxyproto"

	"mikan/internal/nodeapi"
)

// backend is an inbound behind the router: it reads the PROXY header and the first bytes
// after it, and says what it got.
type backendHit struct {
	src   netip.AddrPort
	first byte
}

func backend(t *testing.T) (port string, hits chan backendHit) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	hits = make(chan backendHit, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				h, err := proxyproto.Read(c)
				if err != nil {
					return
				}
				b := make([]byte, 1)
				if _, err := io.ReadFull(c, b); err != nil {
					return
				}
				hits <- backendHit{src: h.Source, first: b[0]}
			}()
		}
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, hits
}

// The router hands a ClientHello to the inbound its name routes to, with the client's
// address in a PROXY v2 header the core reads, the ClientHello whole after it; another
// name goes to the default inbound; with none, it is closed.
func TestSNIRouter(t *testing.T) {
	aPort, aHits := backend(t)
	bPort, bHits := backend(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &sniRouter{ln: ln, log: slog.New(slog.DiscardHandler)}
	r.set(&nodeapi.SNIRouter{Routes: []nodeapi.SNIRoute{{Inbound: "a", Names: []string{"A.example.com."}}}, Default: "b"},
		map[string]string{"a": aPort, "b": bPort})
	go r.serve(nil)
	defer r.close()
	hello := func(name string) net.Conn {
		t.Helper()
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		// The handshake cannot finish (the backend speaks no TLS): only the ClientHello
		// counts, so it is started and left.
		go func() { _ = tls.Client(c, &tls.Config{ServerName: name, InsecureSkipVerify: true}).Handshake() }()
		return c
	}
	wait := func(hits chan backendHit, c net.Conn) {
		t.Helper()
		select {
		case h := <-hits:
			want, _ := netip.ParseAddrPort(c.LocalAddr().String())
			if h.src != want || h.first != 0x16 {
				t.Fatalf("backend got src %v first %#x, want %v and a TLS record", h.src, h.first, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("nothing reached the backend")
		}
	}
	c := hello("a.example.com")
	wait(aHits, c)
	c.Close()
	c = hello("unknown.example.org")
	wait(bHits, c)
	c.Close()

	r.set(&nodeapi.SNIRouter{Routes: []nodeapi.SNIRoute{{Inbound: "a", Names: []string{"a.example.com"}}}}, map[string]string{"a": aPort})
	plain, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	_, _ = plain.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	_ = plain.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := plain.Read(make([]byte, 1)); err == nil || n != 0 {
		t.Fatalf("a request that is no TLS, with no default: read %d, %v", n, err)
	}
}

func TestProxyV2Parses(t *testing.T) {
	for _, c := range []struct{ src, dst string }{{"203.0.113.7:51234", "198.51.100.1:443"}, {"[2001:db8::7]:51234", "[2001:db8::1]:443"}} {
		src, dst := netip.MustParseAddrPort(c.src), netip.MustParseAddrPort(c.dst)
		pr, pw := net.Pipe()
		go func() { _, _ = pw.Write(proxyV2(src, dst)); pw.Close() }()
		h, err := proxyproto.Read(pr)
		if err != nil || h.Version != 2 || h.Local || h.Source != src || h.Destination != dst {
			t.Fatalf("%s: %+v %v", c.src, h, err)
		}
	}
}
