// Package nodeapi is the contract between the panel and a node. It is shared by both
// binaries and must not import mihomo.
package nodeapi

import (
	"encoding/json"
	"time"

	"mikan/internal/proto"
)

const (
	PresetVlessVision = "vless_reality_vision"
	PresetVlessXHTTP  = "vless_reality_xhttp"
	PresetHysteria2   = "hysteria2"
	PresetTUIC        = "tuic_v5"
)

// DesiredState is the complete configuration of a node. Applying the same state twice
// is a no-op; listeners are recreated only when their own part changed.
type DesiredState struct {
	Revision int64     `json:"revision"`
	Epoch    string    `json:"epoch"` // counters epoch the policies' BaseSeq refers to
	Inbounds []Inbound `json:"inbounds"`
	Slots    []Slot    `json:"slots"`
	Policies []Policy  `json:"policies"`
	TLS      *TLSFiles `json:"tls,omitempty"`
	// SelfStealPort allows REALITY dest 127.0.0.1:<port> (the panel's own HTTPS).
	SelfStealPort int `json:"self_steal_port,omitempty"`
}

type Inbound struct {
	Name   string          `json:"name"`
	Listen string          `json:"listen"`
	Port   string          `json:"port"`             // "443" or a range "20000-20100"
	Config json.RawMessage `json:"config,omitempty"` // proto.Template as JSON
	// Preset and Settings are the format of mikan ≤ 0.1.2; the node still reads them
	// from a saved state, the panel no longer sends them.
	Preset   string          `json:"preset,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

type Slot = proto.Slot

// ValidateRequest asks the node to parse one inbound with mihomo without applying it.
type ValidateRequest struct {
	Inbound       Inbound `json:"inbound"`
	SelfStealPort int     `json:"self_steal_port,omitempty"`
}

type Policy struct {
	Slot           string   `json:"slot"`
	Allowed        bool     `json:"allowed"`
	Inbounds       []string `json:"inbounds,omitempty"` // allowed inbound names; empty = all
	DeviceLimit    int      `json:"device_limit"`       // 0 = unlimited
	QuotaRemaining int64    `json:"quota_remaining"`    // bytes left as of BaseSeq; -1 = unlimited
	BaseSeq        int64    `json:"base_seq"`
	// OtherIPs are the slot's devices on the panel's other nodes: they count against
	// DeviceLimit here too, and may connect here without taking another device.
	OtherIPs []string `json:"other_ips,omitempty"`
}

type PoliciesRequest struct {
	Epoch    string   `json:"epoch"`
	Policies []Policy `json:"policies"`
}

type KickRequest struct {
	Slots []string `json:"slots"`
}

type AckRequest struct {
	Epoch string `json:"epoch"`
	Seq   int64  `json:"seq"`
}

type TLSFiles struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// Counters is a batch of traffic deltas. The node returns the same batch until it is
// acknowledged, so the panel can apply it idempotently by (Epoch, Seq).
type Counters struct {
	Epoch  string             `json:"epoch"`
	Seq    int64              `json:"seq"`
	Slots  map[string]Traffic `json:"slots"`
	Online map[string]Online  `json:"online"` // live view, not part of the batch
}

type Traffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

type Online struct {
	IPs   []string `json:"ips"`
	Conns int      `json:"conns"`
}

type Health struct {
	Version   string           `json:"version"`
	Core      string           `json:"core"`
	Revision  int64            `json:"revision"`
	StartedAt time.Time        `json:"started_at"`
	Listeners []ListenerStatus `json:"listeners"`
	Conns     int              `json:"conns"`
	System    System           `json:"system"`
}

type System struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemTotal   uint64  `json:"mem_total"`
	MemUsed    uint64  `json:"mem_used"`
	ProcRSS    uint64  `json:"proc_rss"`
	NetRxBps   uint64  `json:"net_rx_bps"`
	NetTxBps   uint64  `json:"net_tx_bps"`
}

type ListenerStatus struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type ApplyResult struct {
	Revision  int64            `json:"revision"`
	Recreated []string         `json:"recreated"`
	Listeners []ListenerStatus `json:"listeners"`
}

type LogLine struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Error struct {
	Code    string `json:"code"` // invalid_state | apply_failed | not_ready | bad_request
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Preset settings stored in inbounds.settings and passed to the node as-is.

type RealitySettings struct {
	PrivateKey  string   `json:"private_key"`
	PublicKey   string   `json:"public_key"`
	ShortIDs    []string `json:"short_ids"`
	Dest        string   `json:"dest"`
	ServerNames []string `json:"server_names"`
}

type VlessVisionSettings struct {
	Reality RealitySettings `json:"reality"`
}

type VlessXHTTPSettings struct {
	Reality RealitySettings `json:"reality"`
	Path    string          `json:"path"`
	Mode    string          `json:"mode"` // "stream-one": "auto" hangs on mihomo v1.19.31 (S-01a)
}

type Hysteria2Settings struct {
	ObfsPassword string `json:"obfs_password,omitempty"`
	UpMbps       int    `json:"up_mbps,omitempty"`
	DownMbps     int    `json:"down_mbps,omitempty"`
	Masquerade   string `json:"masquerade,omitempty"`
}

type TUICSettings struct {
	CongestionControl string `json:"congestion_control"`
}
