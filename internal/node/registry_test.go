package node

import (
	"testing"
	"time"

	"mikan/internal/nodeapi"
)

// A device limit is shared by all nodes of a panel: devices the panel saw on other nodes
// take places here, and those same devices may still connect here.
func TestDeviceLimitCountsOtherNodes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := NewRegistry("e1", 0, time.Minute, func() time.Time { return now })
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	policy := nodeapi.Policy{Slot: "s1", Allowed: true, DeviceLimit: 2, QuotaRemaining: -1, OtherIPs: []string{"198.51.100.1"}}
	r.SetPolicies("e1", []nodeapi.Policy{policy})

	if r.admit("u1", "vless", "203.0.113.1", false) == nil {
		t.Fatal("the second device must connect")
	}
	if r.admit("u1", "vless", "203.0.113.2", false) != nil {
		t.Fatal("a third device must be refused: one is already online on another node")
	}
	if r.admit("u1", "vless", "198.51.100.1", false) == nil {
		t.Fatal("the device from the other node must connect here without taking a place")
	}

	// Without devices elsewhere the limit is this node's alone.
	policy.OtherIPs = nil
	r.SetPolicies("e1", []nodeapi.Policy{policy})
	now = now.Add(2 * time.Minute) // both local devices are released
	for _, ip := range []string{"203.0.113.3", "203.0.113.4"} {
		if r.admit("u1", "vless", ip, false) == nil {
			t.Fatalf("%s must connect", ip)
		}
	}
	if r.admit("u1", "vless", "203.0.113.5", false) != nil {
		t.Fatal("the limit still holds on one node")
	}
}
