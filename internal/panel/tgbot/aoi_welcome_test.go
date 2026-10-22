package tgbot

import "testing"

func TestAoiWelcome(t *testing.T) {
	in := "Добро пожаловать!\n\n• 1 месяц — 160 ₽\n\n\n"
	if got := aoiWelcome(in); got != "Добро пожаловать!\n\n• 1 месяц — 160 ₽" {
		t.Errorf("aoiWelcome = %q", got)
	}
	mid := "A\n\n\n\nB"
	if got := aoiWelcome(mid); got != "A\n\nB" {
		t.Errorf("aoiWelcome = %q", got)
	}
}
