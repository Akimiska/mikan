package filters

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"mikan/internal/nodeapi"
)

func TestValidate(t *testing.T) {
	c := Config{
		Egress: Egress{Enabled: true,
			Ports:    []string{"6889-6881", "465", "465", "6881-6881", "1000 - 2000"},
			Networks: []string{"203.0.113.7/24", "198.51.100.9", "::ffff:198.51.100.9", "2001:DB8::1/32"},
			Domains:  []string{"*.Mail.Example", ".spam.example.", "mail.example"},
		},
		Ingress: Ingress{Enabled: true, Networks: []string{"10.0.0.1"}},
	}
	err := c.Validate()
	var p *Problem
	if !errors.As(err, &p) || p.List != "egress.ports" || p.Index != 0 {
		t.Fatalf("a reversed range: %v", err)
	}
	c.Egress.Ports[0] = "6881-6889"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	want := Egress{Enabled: true,
		Ports:    []string{"1000-2000", "465", "6881", "6881-6889"},
		Networks: []string{"198.51.100.9/32", "2001:db8::/32", "203.0.113.0/24"},
		Domains:  []string{"mail.example", "spam.example"},
	}
	if !reflect.DeepEqual(c.Egress, want) {
		t.Fatalf("canonical: %+v", c.Egress)
	}
	if c.Ingress.Networks[0] != "10.0.0.1/32" {
		t.Fatalf("ingress: %v", c.Ingress.Networks)
	}

	for _, bad := range []struct {
		list string
		c    Config
	}{
		{"egress.ports", Config{Egress: Egress{Ports: []string{"0"}}}},
		{"egress.ports", Config{Egress: Egress{Ports: []string{"+465"}}}}, // mihomo refuses the sign
		{"egress.networks", Config{Egress: Egress{Networks: []string{"300.1.1.1"}}}},
		{"egress.domains", Config{Egress: Egress{Domains: []string{"bad,REJECT"}}}},
		{"egress.domains", Config{Egress: Egress{Domains: []string{"203.0.113.7"}}}}, // an address goes to the networks
		{"ingress.networks", Config{Ingress: Ingress{Networks: []string{"fe80::1%eth0"}}}},
	} {
		if err := bad.c.Validate(); !errors.As(err, &p) || p.List != bad.list {
			t.Errorf("%s: %v", bad.list, err)
		}
	}
	// The answer names a wrong entry without sending a pasted page back whole.
	huge := Config{Egress: Egress{Domains: []string{strings.Repeat("я", 5000)}}}
	if err := huge.Validate(); !errors.As(err, &p) || len([]rune(p.Value)) > 65 {
		t.Fatalf("the wrong entry comes back whole: %d", len(p.Value))
	}
	// An IPv4 network written as IPv6 is the IPv4 one: the node sees clients that way.
	mapped := Config{Ingress: Ingress{Networks: []string{"::ffff:203.0.113.0/120"}}}
	if err := mapped.Validate(); err != nil || mapped.Ingress.Networks[0] != "203.0.113.0/24" {
		t.Fatalf("mapped: %v %v", mapped.Ingress.Networks, err)
	}
	long := Config{Egress: Egress{Ports: make([]string, MaxItems+1)}}
	if err := long.Validate(); !errors.Is(err, ErrConfig) {
		t.Fatalf("too long: %v", err)
	}
}

func TestState(t *testing.T) {
	d := Default()
	if d.State() != nil {
		t.Fatal("off by default")
	}
	d.Egress.Enabled = true // the mail preset is on by default
	if got := d.State(); got == nil || !reflect.DeepEqual(got.Egress.Ports, []string{"2525", "465", "587"}) || len(got.Ingress.Networks) != 0 {
		t.Fatalf("mail: %+v", got)
	}
	d.Egress.Mail = false
	if d.State() != nil {
		t.Fatal("on with nothing to do is no filter")
	}
	d.Ingress = Ingress{Enabled: true, Allow: true, Networks: []string{"203.0.113.0/24"}}
	if got := d.State(); got == nil || !reflect.DeepEqual(got.Ingress, nodeapi.Ingress{Allow: true, Networks: []string{"203.0.113.0/24"}}) {
		t.Fatalf("ingress: %+v", got)
	}
	d.Ingress.Enabled = false
	if d.State() != nil {
		t.Fatal("ingress off")
	}
}
