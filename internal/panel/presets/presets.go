// Package presets knows how to create and describe the supported inbound types.
package presets

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/secure"
)

type Info struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Network string `json:"network"` // tcp | udp
	Port    string `json:"default_port"`
	Name    string `json:"default_name"`
}

var All = []Info{
	{ID: nodeapi.PresetVlessVision, Title: "VLESS · REALITY · Vision", Summary: "Основной для РФ: маскировка под чужой сайт, домен не нужен", Network: "tcp", Port: "443", Name: "vless-vision"},
	{ID: nodeapi.PresetVlessXHTTP, Title: "VLESS · REALITY · XHTTP", Summary: "Запасной TCP-вариант: трафик похож на обычный HTTP", Network: "tcp", Port: "8443", Name: "vless-xhttp"},
	{ID: nodeapi.PresetHysteria2, Title: "Hysteria2", Summary: "Быстрый на плохих каналах, работает по UDP", Network: "udp", Port: "443", Name: "hysteria2"},
	{ID: nodeapi.PresetTUIC, Title: "TUIC v5", Summary: "Альтернатива на QUIC, тоже по UDP", Network: "udp", Port: "8443", Name: "tuic"},
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

// NewSettings generates fresh keys and secrets for a preset.
func NewSettings(preset, dest string) (json.RawMessage, error) {
	if dest == "" {
		dest = DefaultDest
	}
	var v any
	switch preset {
	case nodeapi.PresetVlessVision, nodeapi.PresetVlessXHTTP:
		r, err := NewReality(dest)
		if err != nil {
			return nil, err
		}
		if preset == nodeapi.PresetVlessVision {
			v = nodeapi.VlessVisionSettings{Reality: r}
		} else {
			v = nodeapi.VlessXHTTPSettings{Reality: r, Path: "/" + strings.ToLower(secure.Token(10)), Mode: "stream-one"}
		}
	case nodeapi.PresetHysteria2:
		v = nodeapi.Hysteria2Settings{ObfsPassword: secure.Token(24)}
	case nodeapi.PresetTUIC:
		v = nodeapi.TUICSettings{CongestionControl: "bbr"}
	default:
		return nil, fmt.Errorf("unknown preset %q", preset)
	}
	return json.Marshal(v)
}

func NewReality(dest string) (nodeapi.RealitySettings, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nodeapi.RealitySettings{}, err
	}
	sid := make([]byte, 4)
	if _, err := rand.Read(sid); err != nil {
		return nodeapi.RealitySettings{}, err
	}
	host := dest
	if i := strings.LastIndex(dest, ":"); i > 0 {
		host = dest[:i]
	}
	b64 := base64.RawURLEncoding
	return nodeapi.RealitySettings{
		PrivateKey: b64.EncodeToString(k.Bytes()), PublicKey: b64.EncodeToString(k.PublicKey().Bytes()),
		ShortIDs: []string{hex.EncodeToString(sid)}, Dest: dest, ServerNames: []string{host},
	}, nil
}

// Reality extracts the REALITY part of a VLESS inbound's settings.
func Reality(preset string, settings []byte) (nodeapi.RealitySettings, error) {
	switch preset {
	case nodeapi.PresetVlessVision:
		var s nodeapi.VlessVisionSettings
		err := json.Unmarshal(settings, &s)
		return s.Reality, err
	case nodeapi.PresetVlessXHTTP:
		var s nodeapi.VlessXHTTPSettings
		err := json.Unmarshal(settings, &s)
		return s.Reality, err
	}
	return nodeapi.RealitySettings{}, fmt.Errorf("%s has no REALITY settings", preset)
}
