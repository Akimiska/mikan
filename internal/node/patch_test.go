package node

import (
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/metacubex/mihomo/listener"

	"mikan/internal/nodeapi"
	"mikan/internal/nodetls"
)

// The core reports what an Apply did to the listeners: a port another program holds is a
// busy-port status, a change of the users keeps the running listener (its other users'
// connections stay) and is no recreation, and a listener that could not bind starts once
// the port is free.
func TestApplyListenersThroughTheCore(t *testing.T) {
	e, err := Start(Options{DataDir: t.TempDir(), Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.Apply(nodeapi.DesiredState{Revision: 99}) })
	pair, err := nodetls.Generate("node.test", x509.ExtKeyUsageServerAuth, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	free := freePort(t)
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	busyPort := strconv.Itoa(busy.Addr().(*net.TCPAddr).Port)
	ws := []byte(`{"type":"vless","ws-path":"/w","mikan":{"tls":"node"}}`)
	s1 := nodeapi.Slot{Name: "s1", UUID: "00000000-0000-4000-8000-000000000001", Secret: "a"}
	s2 := nodeapi.Slot{Name: "s2", UUID: "00000000-0000-4000-8000-000000000002", Secret: "b"}
	state := func(rev int64, slots ...nodeapi.Slot) nodeapi.DesiredState {
		return nodeapi.DesiredState{
			Revision: rev,
			Inbounds: []nodeapi.Inbound{{Name: "ws-free", Port: free, Config: ws}, {Name: "ws-busy", Port: busyPort, Config: ws}},
			Slots:    slots,
			TLS:      &nodeapi.TLSFiles{CertPEM: pair.CertPEM, KeyPEM: pair.KeyPEM},
		}
	}

	res, err := e.Apply(state(1, s1))
	if err != nil {
		t.Fatal(err)
	}
	st := statusOf(res.Listeners)
	if !st["ws-free"].OK || st["ws-busy"].OK || st["ws-busy"].Code != nodeapi.ListenerAddrInUse {
		t.Fatalf("listeners: %+v", res.Listeners)
	}
	if !slices.Equal(res.Recreated, []string{"ws-free"}) {
		t.Fatalf("recreated on the first apply: %v", res.Recreated)
	}
	running, ok := listener.GetInbound("ws-free")
	if !ok {
		t.Fatal("ws-free is not running")
	}

	res, err = e.Apply(state(2, s1, s2))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Recreated) != 0 {
		t.Fatalf("a new user recreated listeners: %v", res.Recreated)
	}
	if now, _ := listener.GetInbound("ws-free"); now != running {
		t.Fatal("a new user replaced the running listener")
	}
	users, err := listener.InboundUsers("ws-free")
	if err != nil || len(users) != 2 {
		t.Fatalf("users in place: %v %v", users, err)
	}
	if st := statusOf(res.Listeners); st["ws-busy"].Code != nodeapi.ListenerAddrInUse {
		t.Fatalf("the busy port is still reported: %+v", res.Listeners)
	}

	_ = busy.Close()
	res, err = e.Apply(state(3, s1, s2))
	if err != nil {
		t.Fatal(err)
	}
	if st := statusOf(res.Listeners); !st["ws-busy"].OK || !slices.Equal(res.Recreated, []string{"ws-busy"}) {
		t.Fatalf("after the port is free: %+v, recreated %v", res.Listeners, res.Recreated)
	}
}

func statusOf(ls []nodeapi.ListenerStatus) map[string]nodeapi.ListenerStatus {
	m := map[string]nodeapi.ListenerStatus{}
	for _, l := range ls {
		m[l.Name] = l
	}
	return m
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}
