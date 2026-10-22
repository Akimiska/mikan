package subs

import "testing"

func TestClientRules(t *testing.T) {
	type want struct{ client, rules bool }
	for ua, w := range map[string]want{
		"AoiVPN/v0.3.0 FlClash X/v0.3.0 core/v1.19.32 Platform/android": {true, true},
		"clash-verge/v2.2.3":                                          {true, true},
		"sing-box/1.14.2":                                             {true, true},
		"SFA (sing-box 1.14.2; language en_US)":                       {true, true},
		"v2rayNG/2.3.11":                                              {true, false},
		"Happ/4.7.1/Android/17912103176801703550":                     {true, false},
		"INCY/2.6.3/ios CFNetwork/3860.700.2 Darwin/25":               {true, false},
		"Shadowrocket/2070 CFNetwork/1410.0.3 Darwin/22.6.0":          {true, false},
		"Mozilla/5.0 (Linux; arm_64; Android 13)":                     {false, false},
		"Mozilla/5.0 (compatible; vkShare; +http://vk.com/dev/Share)": {false, false},
		"curl/8.18.0":                                                 {false, false},
		"Infinix":                                                     {false, false},
	} {
		if c, r := ClientRules(ua); c != w.client || r != w.rules {
			t.Errorf("ClientRules(%q) = %v,%v, want %v,%v", ua, c, r, w.client, w.rules)
		}
	}
}
