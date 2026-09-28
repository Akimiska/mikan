// Package subs renders subscriptions: share links for Xray/sing-box based clients and a
// mihomo profile for Clash-family clients.
package subs

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/store/db"
)

// Endpoint describes how clients reach the node.
type Endpoint struct {
	Host      string // IP or domain the client connects to
	SNI       string // TLS server name for Hysteria2/TUIC; empty for IP-only installs
	PinSHA256 string // hex SHA-256 of the certificate when it is self-signed
}

type Profile struct {
	Slot     db.Slot
	Inbounds []db.Inbound // enabled and allowed for this user, in display order
	Endpoint Endpoint
}

type proxy struct {
	name string
	uri  string
	yaml map[string]any
}

func build(p Profile) ([]proxy, error) {
	var out []proxy
	host := p.Endpoint.Host
	for _, in := range p.Inbounds {
		port, err := firstPort(in.Port)
		if err != nil {
			return nil, err
		}
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		switch in.Preset {
		case nodeapi.PresetVlessVision:
			var s nodeapi.VlessVisionSettings
			if err := json.Unmarshal([]byte(in.Settings), &s); err != nil {
				return nil, err
			}
			name := "VLESS Vision"
			q := realityQuery(s.Reality)
			q.Set("flow", "xtls-rprx-vision")
			q.Set("type", "tcp")
			out = append(out, proxy{name: name,
				uri: "vless://" + p.Slot.Uuid + "@" + addr + "?" + q.Encode() + "#" + url.PathEscape(name),
				yaml: map[string]any{"name": name, "type": "vless", "server": host, "port": port, "uuid": p.Slot.Uuid,
					"network": "tcp", "tls": true, "udp": true, "flow": "xtls-rprx-vision",
					"servername": s.Reality.ServerNames[0], "client-fingerprint": "chrome",
					"reality-opts": map[string]any{"public-key": s.Reality.PublicKey, "short-id": s.Reality.ShortIDs[0]}}})
		case nodeapi.PresetVlessXHTTP:
			var s nodeapi.VlessXHTTPSettings
			if err := json.Unmarshal([]byte(in.Settings), &s); err != nil {
				return nil, err
			}
			name := "VLESS XHTTP"
			q := realityQuery(s.Reality)
			q.Set("type", "xhttp")
			q.Set("path", s.Path)
			q.Set("mode", s.Mode)
			out = append(out, proxy{name: name,
				uri: "vless://" + p.Slot.Uuid + "@" + addr + "?" + q.Encode() + "#" + url.PathEscape(name),
				yaml: map[string]any{"name": name, "type": "vless", "server": host, "port": port, "uuid": p.Slot.Uuid,
					"network": "xhttp", "tls": true, "udp": false,
					"servername": s.Reality.ServerNames[0], "client-fingerprint": "chrome",
					"reality-opts": map[string]any{"public-key": s.Reality.PublicKey, "short-id": s.Reality.ShortIDs[0]},
					"xhttp-opts":   map[string]any{"path": s.Path, "mode": s.Mode}}})
		case nodeapi.PresetHysteria2:
			var s nodeapi.Hysteria2Settings
			if err := json.Unmarshal([]byte(in.Settings), &s); err != nil {
				return nil, err
			}
			name := "Hysteria2"
			q := url.Values{}
			y := map[string]any{"name": name, "type": "hysteria2", "server": host, "port": port, "password": p.Slot.Secret, "alpn": []string{"h3"}}
			if in.Port != strconv.Itoa(port) {
				q.Set("mport", in.Port)
				y["ports"] = in.Port
			}
			if s.ObfsPassword != "" {
				q.Set("obfs", "salamander")
				q.Set("obfs-password", s.ObfsPassword)
				y["obfs"], y["obfs-password"] = "salamander", s.ObfsPassword
			}
			tlsParams(p.Endpoint, q, y, "insecure", "pinSHA256")
			out = append(out, proxy{name: name,
				uri:  "hysteria2://" + url.PathEscape(p.Slot.Secret) + "@" + addr + "/?" + q.Encode() + "#" + url.PathEscape(name),
				yaml: y})
		case nodeapi.PresetTUIC:
			var s nodeapi.TUICSettings
			if err := json.Unmarshal([]byte(in.Settings), &s); err != nil {
				return nil, err
			}
			name := "TUIC"
			cc := s.CongestionControl
			if cc == "" {
				cc = "bbr"
			}
			q := url.Values{"congestion_control": {cc}, "alpn": {"h3"}, "udp_relay_mode": {"native"}}
			y := map[string]any{"name": name, "type": "tuic", "server": host, "port": port, "uuid": p.Slot.Uuid,
				"password": p.Slot.Secret, "alpn": []string{"h3"}, "congestion-controller": cc, "udp-relay-mode": "native"}
			tlsParams(p.Endpoint, q, y, "allow_insecure", "")
			out = append(out, proxy{name: name,
				uri:  "tuic://" + p.Slot.Uuid + ":" + url.PathEscape(p.Slot.Secret) + "@" + addr + "?" + q.Encode() + "#" + url.PathEscape(name),
				yaml: y})
		}
	}
	return out, nil
}

func realityQuery(r nodeapi.RealitySettings) url.Values {
	return url.Values{
		"encryption": {"none"}, "security": {"reality"}, "sni": {r.ServerNames[0]},
		"fp": {"chrome"}, "pbk": {r.PublicKey}, "sid": {r.ShortIDs[0]},
	}
}

// tlsParams: with a self-signed certificate the link pins its fingerprint; with a real
// certificate the client verifies it normally.
func tlsParams(ep Endpoint, q url.Values, y map[string]any, insecureKey, pinKey string) {
	if ep.SNI != "" {
		q.Set("sni", ep.SNI)
		y["sni"] = ep.SNI
	}
	if ep.PinSHA256 == "" {
		return
	}
	q.Set(insecureKey, "1")
	if pinKey != "" {
		q.Set(pinKey, ep.PinSHA256)
	}
	y["fingerprint"] = ep.PinSHA256
	y["skip-cert-verify"] = false
}

func firstPort(spec string) (int, error) {
	head, _, _ := strings.Cut(spec, ",")
	head, _, _ = strings.Cut(head, "-")
	p, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil || p <= 0 || p > 65535 {
		return 0, fmt.Errorf("bad port %q", spec)
	}
	return p, nil
}

// URIs renders one share link per inbound.
func URIs(p Profile) (string, error) {
	ps, err := build(p)
	if err != nil {
		return "", err
	}
	lines := make([]string, len(ps))
	for i, x := range ps {
		lines[i] = x.uri
	}
	return strings.Join(lines, "\n"), nil
}

// Mihomo renders a complete client profile. mihomo's parser accepts JSON as YAML.
func Mihomo(p Profile, rules []string) ([]byte, error) {
	ps, err := build(p)
	if err != nil {
		return nil, err
	}
	proxies := make([]map[string]any, len(ps))
	names := make([]string, len(ps))
	for i, x := range ps {
		proxies[i], names[i] = x.yaml, x.name
	}
	cfg := map[string]any{
		"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "warning",
		"ipv6": true, "unified-delay": true, "tcp-concurrent": true,
		"dns": map[string]any{
			"enable": true, "ipv6": true, "enhanced-mode": "fake-ip", "fake-ip-range": "198.18.0.1/16",
			"default-nameserver": []string{"1.1.1.1", "8.8.8.8"},
			"nameserver":         []string{"https://1.1.1.1/dns-query", "https://dns.google/dns-query"},
		},
		"proxies": proxies,
		"proxy-groups": []map[string]any{
			{"name": "VPN", "type": "select", "proxies": append([]string{"Авто"}, names...)},
			{"name": "Авто", "type": "url-test", "proxies": names, "url": "https://www.gstatic.com/generate_204", "interval": 300, "tolerance": 50},
		},
		"rules": rules,
	}
	return json.MarshalIndent(cfg, "", "  ")
}
