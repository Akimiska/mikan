package nodetls

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func handshake(t *testing.T, server, client *tls.Config) error {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", server)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		if err := c.(*tls.Conn).Handshake(); err == nil {
			_, _ = c.Write([]byte("ok"))
		}
	}()
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", ln.Addr().String(), client)
	if err != nil {
		return err
	}
	defer c.Close()
	// TLS 1.3 reports a rejected client certificate on the first read.
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2)
	_, err = io.ReadFull(c, buf)
	return err
}

func TestPinnedHandshake(t *testing.T) {
	now := time.Now()
	panel, err := Generate("mikan-panel", x509.ExtKeyUsageClientAuth, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := Generate("node-2.mikan", x509.ExtKeyUsageServerAuth, now)
	if err != nil {
		t.Fatal(err)
	}
	panelPin, _ := Fingerprint(panel.CertPEM)
	nodePin, _ := Fingerprint(node.CertPEM)

	raw, err := Key{Port: 40123, PanelPin: panelPin, CertPEM: node.CertPEM, KeyPEM: node.KeyPEM}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(raw, " '\"$`\n") {
		t.Fatalf("the key must paste into a shell as one word: %q", raw)
	}
	key, err := DecodeKey(raw)
	if err != nil || key.Port != 40123 {
		t.Fatalf("decode: %+v %v", key, err)
	}
	server, err := key.ServerConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := ClientConfig(panel, nodePin)
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, server, client); err != nil {
		t.Fatalf("panel must reach its node: %v", err)
	}

	// Another panel (or a leaked node key used as a client) is refused by the node.
	stranger, _ := Generate("mikan-panel", x509.ExtKeyUsageClientAuth, now)
	other, _ := ClientConfig(stranger, nodePin)
	if handshake(t, server, other) == nil {
		t.Fatal("a foreign client certificate must be refused")
	}
	asClient, _ := ClientConfig(node, nodePin)
	if handshake(t, server, asClient) == nil {
		t.Fatal("the node's own certificate must not work as the panel's")
	}
	// The panel refuses a node whose certificate is not the pinned one (a re-issued key).
	fresh, _ := Generate("node-2.mikan", x509.ExtKeyUsageServerAuth, now)
	impostor, _ := Key{Port: 40123, PanelPin: panelPin, CertPEM: fresh.CertPEM, KeyPEM: fresh.KeyPEM}.ServerConfig()
	if handshake(t, impostor, client) == nil {
		t.Fatal("a node certificate that is not pinned must be refused")
	}
}

func TestDecodeKeyRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "mikan1.", "mikan1.!!!", "abc", "mikan1.eyJwb3J0IjowfQ"} {
		if _, err := DecodeKey(s); err == nil {
			t.Errorf("%q must be refused", s)
		}
	}
}
