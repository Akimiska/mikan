// Package presets is the catalog of ready inbounds: each preset generates a listener
// template (see internal/proto) with fresh keys, paths and passwords.
package presets

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"mikan/internal/panel/secure"
	"mikan/internal/proto"
)

type Info struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"` // Russian fallback; the UI translates presets.<id>
	Type    string `json:"type"`    // mihomo listener type
	Network string `json:"network"` // tcp | udp
	Port    string `json:"default_port"`
	Name    string `json:"default_name"`
	SubName string `json:"sub_name"` // proxy name in subscriptions unless the admin sets one
	Default bool   `json:"default"`  // created on a fresh install
}

// Custom is the "own config" entry: the admin writes the template in the editor.
const Custom = "custom"

// Order is display and fallback order. Since 2026 the RU DPI freezes a server's 443/tcp
// after bursts of parallel TLS handshakes; Vision opens one handshake per app connection,
// so 443/tcp goes to XHTTP, which clients multiplex over a few long-lived connections.
var All = []Info{
	{ID: "vless_reality_xhttp", Title: "VLESS · REALITY · XHTTP", Summary: "Основной для РФ: похож на обычный HTTPS, держит мало соединений", Type: "vless", Network: "tcp", Port: "443", Name: "vless-xhttp", SubName: "VLESS XHTTP", Default: true},
	{ID: "hysteria2", Title: "Hysteria2", Summary: "Быстрый на плохих каналах, работает по UDP", Type: "hysteria2", Network: "udp", Port: "443", Name: "hysteria2", SubName: "Hysteria2", Default: true},
	{ID: "tuic_v5", Title: "TUIC v5", Summary: "Альтернатива на QUIC, тоже по UDP", Type: "tuic", Network: "udp", Port: "8443", Name: "tuic", SubName: "TUIC", Default: true},
	{ID: "vless_reality_vision", Title: "VLESS · REALITY · Vision", Summary: "Для старых клиентов без XHTTP. На 443 в РФ быстро замораживается", Type: "vless", Network: "tcp", Port: "8443", Name: "vless-vision", SubName: "VLESS Vision", Default: true},
	{ID: "vless_reality_grpc", Title: "VLESS · REALITY · gRPC", Summary: "HTTP/2 с мультиплексом: мало соединений, другой рисунок трафика", Type: "vless", Network: "tcp", Port: "2053", Name: "vless-grpc", SubName: "VLESS gRPC"},
	{ID: "trojan_reality", Title: "Trojan · REALITY", Summary: "Другой протокол под той же маскировкой — запасной вариант", Type: "trojan", Network: "tcp", Port: "2087", Name: "trojan", SubName: "Trojan"},
	{ID: "anytls", Title: "AnyTLS", Summary: "TLS с паддингом против анализа размеров пакетов; нужен клиент на mihomo или sing-box", Type: "anytls", Network: "tcp", Port: "2083", Name: "anytls", SubName: "AnyTLS"},
	{ID: Custom, Title: "Свой конфиг", Summary: "Шаблон листенера mihomo в редакторе: любой поддерживаемый тип и параметры", Network: "", Port: "", Name: "custom", SubName: "Custom"},
}

func Get(id string) (Info, bool) {
	for _, p := range All {
		if p.ID == id {
			return p, true
		}
	}
	return Info{}, false
}

// DefaultDest is the REALITY target used until the admin picks one.
const DefaultDest = "www.microsoft.com:443"

// NewConfig generates a template with fresh keys for a preset. dest is the REALITY
// target ("" = DefaultDest); other presets ignore it.
func NewConfig(id, dest string) (string, error) {
	if dest == "" {
		dest = DefaultDest
	}
	var t proto.Template
	switch id {
	case "vless_reality_xhttp":
		t = proto.Template{"type": "vless", "xhttp-config": map[string]any{"path": randomPath(), "mode": "stream-one"}}
	case "vless_reality_vision":
		t = proto.Template{"type": "vless", "mikan": map[string]any{"flow": "xtls-rprx-vision"}}
	case "vless_reality_grpc":
		t = proto.Template{"type": "vless", "grpc-service-name": strings.ToLower(secure.Token(8))}
	case "trojan_reality":
		t = proto.Template{"type": "trojan"}
	case "hysteria2":
		return proto.Marshal(proto.Template{"type": "hysteria2", "alpn": []any{"h3"}, "obfs": "salamander", "obfs-password": secure.Token(24)}), nil
	case "tuic_v5":
		return proto.Marshal(proto.Template{"type": "tuic", "alpn": []any{"h3"}, "congestion-controller": "bbr", "max-idle-time": 15000, "authentication-timeout": 1000}), nil
	case "anytls":
		return proto.Marshal(proto.Template{"type": "anytls"}), nil
	case Custom:
		return "", fmt.Errorf("the custom preset takes the admin's config")
	default:
		return "", fmt.Errorf("unknown preset %q", id)
	}
	r, err := NewReality(dest)
	if err != nil {
		return "", err
	}
	t["reality-config"] = r
	return proto.Marshal(t), nil
}

func randomPath() string { return "/" + strings.ToLower(secure.Token(10)) }

// NewReality returns a reality-config section with a fresh X25519 key and short id.
func NewReality(dest string) (map[string]any, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	sid := make([]byte, 4)
	if _, err := rand.Read(sid); err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		host = dest
	}
	return map[string]any{
		"dest": dest, "server-names": []any{host},
		"private-key": base64.RawURLEncoding.EncodeToString(k.Bytes()), "short-id": []any{hex.EncodeToString(sid)},
	}, nil
}

// SetDest points a template's REALITY camouflage at another site. sni is the name
// clients send; "" means the host of dest (a picked neighbor has dest = its IP instead).
func SetDest(t proto.Template, dest, sni string) error {
	r, ok := t["reality-config"].(map[string]any)
	if !ok {
		return &proto.Error{Code: "dest_no_reality", Field: "reality-config"}
	}
	host, port, err := net.SplitHostPort(dest)
	if err != nil || host == "" || port == "" {
		return &proto.Error{Code: "dest_format", Field: "reality-config.dest"}
	}
	if sni == "" {
		if net.ParseIP(host) != nil {
			return &proto.Error{Code: "reality_sni", Field: "reality-config.server-names"}
		}
		sni = host
	}
	r["dest"], r["server-names"] = dest, []any{sni}
	return nil
}

// Dest reads the REALITY target of a template ("" when there is none).
func Dest(t proto.Template) (dest string, serverNames []string) {
	r, ok := t["reality-config"].(map[string]any)
	if !ok {
		return "", nil
	}
	dest, _ = r["dest"].(string)
	if names, ok := r["server-names"].([]any); ok {
		for _, n := range names {
			if s, ok := n.(string); ok {
				serverNames = append(serverNames, s)
			}
		}
	}
	return dest, serverNames
}
