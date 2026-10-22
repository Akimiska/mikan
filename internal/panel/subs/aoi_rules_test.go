package subs

import "testing"

func TestRuleCapable(t *testing.T) {
	for ua, want := range map[string]bool{
		"AoiVPN/v0.3.0 FlClash X/v0.3.0 core/v1.19.32 Platform/android": true,
		"FlClash X/v0.3.0 Platform/windows":                           true,
		"clash-verge/v2.2.3":                                          true,
		"v2rayNG/2.3.11":                                              false,
		"Happ/4.7.1/Android/17912103176801703550":                     false,
		"sing-box/1.14.2":                                             false,
		"SFA (sing-box 1.14.2; language en_US)":                       true,
		"":                                                            false,
	} {
		if got := RuleCapable(ua); got != want {
			t.Errorf("RuleCapable(%q) = %v, want %v", ua, got, want)
		}
	}
}
