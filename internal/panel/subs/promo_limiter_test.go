package subs

import (
	"testing"
	"time"
)

func TestPromoLimiterBlocksByTelegramAccountAndExpires(t *testing.T) {
	var l promoLimiter
	now := time.Unix(1000, 0)
	for i := 0; i < promoAttemptLimit; i++ {
		if !l.allow(11, now) {
			t.Fatal("account blocked before reaching attempt limit")
		}
	}
	if l.allow(11, now) {
		t.Fatal("account was not blocked after reaching attempt limit")
	}
	if !l.allow(12, now) {
		t.Fatal("one Telegram account blocked another")
	}
	if !l.allow(11, now.Add(promoBlockDuration)) {
		t.Fatal("temporary block did not expire")
	}
	if !l.allow(11, now.Add(promoBlockDuration)) {
		t.Fatal("temporary block did not reset its window")
	}
}
