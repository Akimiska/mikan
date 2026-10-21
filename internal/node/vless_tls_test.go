package node

import (
	"testing"

	"github.com/metacubex/mihomo/listener"

	"mikan/internal/nodeapi"
	"mikan/internal/proto"
)

// VLESS on the node certificate, over XHTTP, WebSocket and with Vision, becomes a listener mihomo
// itself accepts; without a certificate the node refuses it instead of serving plain VLESS.
func TestVLESSTLSListener(t *testing.T) {
	slots := []nodeapi.Slot{{Name: "s1", UUID: "00000000-0000-4000-8000-000000000001", Secret: "x"}}
	cert := proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}
	for name, config := range map[string]string{
		"xhttp":  `{"type":"vless","xhttp-config":{"path":"/p","mode":"stream-one"},"mikan":{"tls":"node"}}`,
		"vision": `{"type":"vless","mikan":{"flow":"xtls-rprx-vision","tls":"node"}}`,
		"ws":     `{"type":"vless","ws-path":"/w","mikan":{"tls":"node"}}`,
	} {
		in := nodeapi.Inbound{Name: "vless-tls-" + name, Port: "2443", Config: []byte(config)}
		l, err := listenerFor(in, slots, cert, proto.Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if l["certificate"] != cert.CertPath || l["private-key"] != cert.KeyPath {
			t.Fatalf("%s: %v", name, l)
		}
		if _, err := listener.ParseListener(l); err != nil {
			t.Fatalf("%s: mihomo refuses it: %v", name, err)
		}
		if _, err := listenerFor(in, slots, proto.Cert{}, proto.Options{}); err == nil {
			t.Fatalf("%s: a listener without a certificate", name)
		}
	}
}

// Behind a CDN the listener takes the client's address from the CDN's header: the core
// accepts the trusted proxies the template names, Cloudflare's included.
func TestCDNListenerParses(t *testing.T) {
	slots := []nodeapi.Slot{{Name: "s1", UUID: "00000000-0000-4000-8000-000000000001", Secret: "x"}}
	cert := proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}
	cfg := `{"type":"vless","xhttp-config":{"path":"/p","mode":"packet-up","uplink-http-method":"GET","uplink-data-placement":"header","uplink-data-key":"X-Data"},` +
		`"mikan":{"tls":"node","client":{"server":"cdn.example.com","port":443},"cdn":{"header":"CF-Connecting-IP","trusted":["cloudflare","203.0.113.0/24"]}}}`
	l, err := listenerFor(nodeapi.Inbound{Name: "vless-cdn", Port: "2083", Config: []byte(cfg)}, slots, cert, proto.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if l["trusted-proxy-header"] != "CF-Connecting-IP" {
		t.Fatalf("listener: %v", l)
	}
	if _, err := listener.ParseListener(l); err != nil {
		t.Fatalf("the core refuses it: %v", err)
	}
}
