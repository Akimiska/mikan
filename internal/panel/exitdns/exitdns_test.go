package exitdns

import (
	"errors"
	"slices"
	"testing"
)

func TestValidate(t *testing.T) {
	c := Config{Resolver: ResolverCustom, Servers: []string{" 8.8.8.8 ", "tls://Dns.Google", "8.8.8.8"}}
	if err := c.Validate(); err != nil || !slices.Equal(c.Servers, []string{"8.8.8.8", "tls://dns.google"}) {
		t.Fatalf("%v %v", c.Servers, err)
	}
	c = Config{Resolver: ResolverCustom, Servers: []string{"1.1.1.1", "dhcp://eth0"}}
	var p *Problem
	if err := c.Validate(); !errors.As(err, &p) || p.Index != 1 {
		t.Fatalf("a wrong server: %v", err)
	}
	for _, c := range []Config{
		{Resolver: "opendns"},
		{Resolver: ResolverCustom, Servers: []string{}},
		{Resolver: ResolverHost, Servers: slices.Repeat([]string{"1.1.1.1"}, 9)},
	} {
		if err := c.Validate(); !errors.Is(err, ErrResolver) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestState(t *testing.T) {
	if s := Default().State(); s == nil || !s.Sniff || s.Servers != nil {
		t.Fatalf("default: %+v", s)
	}
	if s := (Config{Resolver: ResolverHost}).State(); s != nil {
		t.Fatalf("off: %+v", s)
	}
	own := Config{Resolver: ResolverCustom, Servers: []string{"tls://dns.google"}}
	if s := own.State(); s == nil || s.Sniff || !slices.Equal(s.Servers, own.Servers) {
		t.Fatalf("own: %+v", s)
	}
	// The own list is kept, a preset is what the nodes get.
	own.Resolver = ResolverCloudflare
	if s := own.State(); !slices.Equal(s.Servers, Presets[ResolverCloudflare]) {
		t.Fatalf("preset: %+v", s)
	}
}
