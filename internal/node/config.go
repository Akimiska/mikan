package node

import (
	"encoding/json"
	"fmt"

	"mikan/internal/nodeapi"
	"mikan/internal/proto"
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
// log-level warning keeps per-connection lines out of mihomo's own output; the ones that
// still come through are dropped by pumpLogs.
func buildConfig(st nodeapi.DesiredState, cert proto.Cert, allowPrivate bool) ([]byte, error) {
	listeners := make([]map[string]any, 0, len(st.Inbounds))
	for _, in := range st.Inbounds {
		l, err := listenerFor(in, st.Slots, cert, proto.Options{SelfStealPort: st.SelfStealPort})
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

func listenerFor(in nodeapi.Inbound, slots []nodeapi.Slot, cert proto.Cert, o proto.Options) (map[string]any, error) {
	t, err := template(in)
	if err != nil {
		return nil, err
	}
	return proto.Listener(t, in.Name, in.Listen, in.Port, slots, cert, o)
}

// sharedListeners are the inbounds with one key for everyone.
func sharedListeners(st nodeapi.DesiredState) []string {
	var out []string
	for _, in := range st.Inbounds {
		if t, err := template(in); err == nil && proto.Shared(t.Type()) {
			out = append(out, in.Name)
		}
	}
	return out
}

// template reads the inbound's listener template; states saved by mikan ≤ 0.1.2 carry
// a preset with its settings instead.
func template(in nodeapi.Inbound) (proto.Template, error) {
	if len(in.Config) > 0 {
		return proto.FromJSON(in.Config)
	}
	return proto.FromPreset(in.Preset, in.Settings)
}
