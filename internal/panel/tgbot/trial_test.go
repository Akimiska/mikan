package tgbot

import (
	"strings"
	"testing"
)

// The trial's texts are plain: every one is there in both languages, and none has a dash.
func TestTrialTexts(t *testing.T) {
	for name, w := range map[string]*words{"ru": &ru, "en": &en} {
		for field, s := range map[string]string{"trial": w.trial, "trialDone": w.trialDone, "trialUsed": w.trialUsed, "trialOff": w.trialOff, "trialOpen": w.trialOpen, "trialFail": w.trialFail,
			"trialPaid": w.trialPaid, "trialTitle": w.trialTitle, "trialNotGiven": w.trialNotGiven} {
			if s == "" || strings.ContainsAny(s, "—–") {
				t.Errorf("%s.%s is empty or has a dash: %q", name, field, s)
			}
		}
	}
}
