package nodeapi

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ExitDNS is the DNS at the exit: a site should see the node's country in its answer,
// whatever DNS the user's device asked. Xray clients (Happ, v2RayTun, v2rayNG) resolve a
// name on the device, often with the DNS of their own country, and send the node the
// address; a site that answers by where its resolver asked from (Google's Gemini) then
// gives an address that refuses the node's country.
//
// Sniff reads the name out of a connection made to a bare address (the TLS server name,
// the HTTP Host, QUIC's) and dials that name instead, resolved by the node. Servers are
// the resolvers the node asks (CanonDNSServer); none: the host's own.
type ExitDNS struct {
	Sniff   bool     `json:"sniff"`
	Servers []string `json:"servers,omitempty"`
}

// MaxDNSServers bounds the resolvers of a node.
const MaxDNSServers = 8

// SniffPorts are the ports the node reads a name on, by protocol: what goes there starts
// with the client's hello. A server-first protocol (SSH, mail) on another port is never
// held waiting for a hello that does not come.
var SniffPorts = struct{ TLS, HTTP, QUIC []string }{
	TLS:  []string{"443", "8443"},
	HTTP: []string{"80", "8080-8880"},
	QUIC: []string{"443", "8443"},
}

// CanonDNSServer takes a resolver as mihomo knows it and returns it in one form:
// "1.1.1.1", "1.1.1.1:5353" or "[2606:4700::1111]" (plain DNS, by address only),
// "udp://", "tcp://", "tls://host[:port]" (DoT), "https://host[:port]/path" (DoH,
// /dns-query when the path is empty), "quic://host[:port]" (DoQ). The host is an address
// or a domain. Nothing else passes: no "#proxy" (the resolver would go out through a
// proxy by name), no query, no user, none of mihomo's other kinds (dhcp, system, rcode).
func CanonDNSServer(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 512 || strings.ContainsAny(s, "# \t") {
		return "", false
	}
	scheme, rest, hasScheme := strings.Cut(s, "://")
	if !hasScheme {
		host, port, ok := hostPort(s)
		if !ok || !isAddr(host) {
			return "", false // plain DNS by name would need DNS to find it
		}
		return joinHostPort(host, port), true
	}
	scheme = strings.ToLower(scheme)
	u, err := url.Parse(scheme + "://" + rest)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" {
		return "", false
	}
	switch scheme {
	case "udp", "tcp", "tls", "quic":
		if u.Path != "" && u.Path != "/" {
			return "", false
		}
	case "https":
	default:
		return "", false
	}
	host, port, ok := hostPort(u.Host)
	if !ok {
		return "", false
	}
	if !isAddr(host) {
		d, ok := CanonDomain(host)
		// CanonDomain also takes "*.example.com" for a filter; a resolver is one host.
		if !ok || d != strings.ToLower(host) {
			return "", false
		}
		host = d
	}
	out := scheme + "://" + joinHostPort(host, port)
	if scheme == "https" {
		p := u.EscapedPath()
		if p == "" || p == "/" {
			p = "/dns-query"
		}
		out += p
	}
	return out, true
}

// hostPort splits "host", "host:port", "[v6]" or "[v6]:port"; port is "" when not given.
// An address comes back in its canonical form, IPv4 in IPv6 unmapped.
func hostPort(s string) (host, port string, ok bool) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, ok := portNum(p)
		if !ok || h == "" {
			return "", "", false
		}
		host, port = h, strconv.Itoa(n)
	} else {
		host = s
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
			if !isAddr(host) {
				return "", "", false
			}
		}
	}
	if host == "" || strings.ContainsAny(host, "[]/%") {
		return "", "", false
	}
	if a, err := netip.ParseAddr(host); err == nil {
		host = a.Unmap().String()
	} else if strings.Contains(host, ":") {
		return "", "", false // a bad port, or IPv6 without brackets around a port
	}
	return host, port, true
}

func isAddr(h string) bool {
	_, err := netip.ParseAddr(h)
	return err == nil
}

func joinHostPort(host, port string) string {
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if a, err := netip.ParseAddr(host); err == nil && a.Is6() {
		return "[" + host + "]"
	}
	return host
}
