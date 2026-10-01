package proto

import "testing"

func TestPublicHost(t *testing.T) {
	for host, want := range map[string]bool{
		"example.com":       true,
		"Example.COM.":      true,
		"8.8.8.8":           true,
		"[2606:4700::1111]": true,
		"localhost":         false,
		"localhost.":        false,
		"LOCALHOST":         false,
		"LocalHost.":        false,
		"app.localhost":     false,
		"app.localhost.":    false,
		"127.0.0.1":         false,
		"127.0.0.1.":        false,
		"10.1.2.3":          false,
		"192.168.0.1":       false,
		"169.254.169.254":   false,
		"100.64.0.1":        false,
		"::1":               false,
		"[::1]":             false,
		"intranet":          false,
		"":                  false,
	} {
		if got := PublicHost(host); got != want {
			t.Errorf("PublicHost(%q) = %v, want %v", host, got, want)
		}
	}
}
