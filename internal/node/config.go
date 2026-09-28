package node

import (
	"encoding/json"
	"fmt"

	"mikan/internal/nodeapi"
)

// Traffic to the node's own networks is refused: without these rules any VPN user could
// reach the host's loopback services or the cloud metadata endpoint (169.254.169.254).
// IP rules without no-resolve also catch domains that resolve to private addresses.
var privateRules = []string{
	"IP-CIDR,0.0.0.0/8,REJECT",
	"IP-CIDR,10.0.0.0/8,REJECT",
	"IP-CIDR,100.64.0.0/10,REJECT",
	"IP-CIDR,127.0.0.0/8,REJECT",
	"IP-CIDR,169.254.0.0/16,REJECT",
	"IP-CIDR,172.16.0.0/12,REJECT",
	"IP-CIDR,192.168.0.0/16,REJECT",
	"IP-CIDR,224.0.0.0/3,REJECT",
	"IP-CIDR6,::1/128,REJECT",
	"IP-CIDR6,fc00::/7,REJECT",
	"IP-CIDR6,fe80::/10,REJECT",
}

func rules(allowPrivate bool) []string {
	var r []string
	if !allowPrivate {
		r = append(r, privateRules...)
	}
	// Outbound SMTP from a shared VPN IP gets the address blacklisted within hours.
	return append(r, "DST-PORT,25,REJECT", "MATCH,DIRECT")
}

// buildConfig renders the mihomo config as JSON, which mihomo's YAML parser accepts.
// log-level warning keeps per-connection lines (with user destinations) out of the logs.
func buildConfig(st nodeapi.DesiredState, certPath, keyPath string, allowPrivate bool) ([]byte, error) {
	listeners := make([]map[string]any, 0, len(st.Inbounds))
	for _, in := range st.Inbounds {
		l, err := listenerFor(in, st.Slots, certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("inbound %s: %w", in.Name, err)
		}
		listeners = append(listeners, l)
	}
	cfg := map[string]any{
		"mode":              "rule",
		"log-level":         "warning",
		"ipv6":              true,
		"allow-lan":         false,
		"mixed-port":        0,
		"find-process-mode": "off",
		"profile":           map[string]any{"store-selected": false, "store-fake-ip": false},
		"dns":               map[string]any{"enable": false},
		"proxies":           []any{},
		"rules":             rules(allowPrivate),
		"listeners":         listeners,
	}
	return json.Marshal(cfg)
}

func listenerFor(in nodeapi.Inbound, slots []nodeapi.Slot, certPath, keyPath string) (map[string]any, error) {
	listen := in.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	l := map[string]any{"name": in.Name, "port": in.Port, "listen": listen}
	switch in.Preset {
	case nodeapi.PresetVlessVision, nodeapi.PresetVlessXHTTP:
		var r nodeapi.RealitySettings
		flow := ""
		if in.Preset == nodeapi.PresetVlessVision {
			var s nodeapi.VlessVisionSettings
			if err := json.Unmarshal(in.Settings, &s); err != nil {
				return nil, err
			}
			r, flow = s.Reality, "xtls-rprx-vision"
		} else {
			var s nodeapi.VlessXHTTPSettings
			if err := json.Unmarshal(in.Settings, &s); err != nil {
				return nil, err
			}
			r = s.Reality
			mode := s.Mode
			if mode == "" {
				mode = "stream-one"
			}
			l["xhttp-config"] = map[string]any{"path": s.Path, "mode": mode}
		}
		if r.PrivateKey == "" || r.Dest == "" || len(r.ServerNames) == 0 {
			return nil, fmt.Errorf("reality needs private_key, dest and server_names")
		}
		users := make([]map[string]any, 0, len(slots))
		for _, s := range slots {
			u := map[string]any{"username": s.Name, "uuid": s.UUID}
			if flow != "" {
				u["flow"] = flow
			}
			users = append(users, u)
		}
		l["type"] = "vless"
		l["users"] = users
		l["reality-config"] = map[string]any{"dest": r.Dest, "private-key": r.PrivateKey, "short-id": r.ShortIDs, "server-names": r.ServerNames}
	case nodeapi.PresetHysteria2:
		var s nodeapi.Hysteria2Settings
		if err := json.Unmarshal(in.Settings, &s); err != nil {
			return nil, err
		}
		if certPath == "" {
			return nil, fmt.Errorf("hysteria2 needs a TLS certificate")
		}
		users := make(map[string]string, len(slots))
		for _, sl := range slots {
			users[sl.Name] = sl.Secret
		}
		l["type"] = "hysteria2"
		l["users"] = users
		l["alpn"] = []string{"h3"}
		l["certificate"], l["private-key"] = certPath, keyPath
		if s.ObfsPassword != "" {
			l["obfs"], l["obfs-password"] = "salamander", s.ObfsPassword
		}
		if s.UpMbps > 0 && s.DownMbps > 0 {
			l["up"], l["down"] = s.UpMbps, s.DownMbps
		}
		if s.Masquerade != "" {
			l["masquerade"] = s.Masquerade
		}
	case nodeapi.PresetTUIC:
		var s nodeapi.TUICSettings
		if err := json.Unmarshal(in.Settings, &s); err != nil {
			return nil, err
		}
		if certPath == "" {
			return nil, fmt.Errorf("tuic needs a TLS certificate")
		}
		users := make(map[string]string, len(slots))
		for _, sl := range slots {
			users[sl.UUID] = sl.Secret
		}
		cc := s.CongestionControl
		if cc == "" {
			cc = "bbr"
		}
		l["type"] = "tuic"
		l["users"] = users
		l["alpn"] = []string{"h3"}
		l["certificate"], l["private-key"] = certPath, keyPath
		l["congestion-controller"] = cc
		l["max-idle-time"] = 15000
		l["authentication-timeout"] = 1000
	default:
		return nil, fmt.Errorf("unknown preset %q", in.Preset)
	}
	return l, nil
}
