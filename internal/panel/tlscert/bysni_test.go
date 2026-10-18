package tlscert

import (
	"crypto/tls"
	"testing"
	"time"
)

// Clients that name the subscription domain get its certificate once it has one; every
// other name, and the domain before its certificate, gets the panel's.
func TestBySNI(t *testing.T) {
	now := time.Now()
	panel, err := LoadOrCreateSelfSigned(t.TempDir(), "panel.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := LoadOrCreateSelfSigned(t.TempDir(), "sub.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	b := &BySNI{Panel: &Holder{}, Sub: &Holder{}}
	b.Panel.Set(panel)
	get := func(name string) *tls.Certificate {
		t.Helper()
		c, err := b.Get(&tls.ClientHelloInfo{ServerName: name})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	b.SetName("sub.example.com")
	if get("sub.example.com") != panel {
		t.Fatal("the domain without a certificate of its own gets the panel's")
	}
	b.Sub.Set(sub)
	if get("SUB.example.com.") != sub || get("panel.example.com") != panel || get("") != panel {
		t.Fatal("certificates by name")
	}
	b.SetName("")
	if get("sub.example.com") != panel {
		t.Fatal("no subscription domain: the panel's for everyone")
	}
}
