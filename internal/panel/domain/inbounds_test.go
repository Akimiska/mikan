package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/settings"
)

func TestAddPreset(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, _, _ := setup(t, &now)
	ctx := context.Background()
	set := settings.New(st.Q)
	info, _ := presets.Get("trojan_reality")

	in, err := AddPreset(ctx, st, set, "trojan_reality", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if in.Port != info.Port || in.Name != info.Name || in.Config == "" || in.Enabled == 0 {
		t.Fatalf("inbound: %+v", in)
	}
	var busy *PortInUseError
	if _, err := AddPreset(ctx, st, set, "trojan_reality", "", now); !errors.As(err, &busy) || busy.Owner != info.Name {
		t.Fatalf("the preset's port is taken now: %v", err)
	}
	second, err := AddPreset(ctx, st, set, "trojan_reality", "20000", now)
	if err != nil || second.Name != info.Name+"-2" {
		t.Fatalf("second inbound: %+v %v", second, err)
	}
	// TCP and UDP listeners share a port number without conflict.
	if _, err := AddPreset(ctx, st, set, "tuic_v5", "20000", now); err != nil {
		t.Fatalf("udp next to tcp: %v", err)
	}
	for _, c := range []struct {
		id, port string
		want     error
	}{
		{"nope", "", ErrUnknownPreset},
		{presets.Custom, "", ErrUnknownPreset},
		{"anytls", "70000", ErrBadPort},
		{"anytls", "2083-2000", ErrBadPort},
	} {
		if _, err := AddPreset(ctx, st, set, c.id, c.port, now); !errors.Is(err, c.want) {
			t.Errorf("%s %q: got %v, want %v", c.id, c.port, err, c.want)
		}
	}
}
