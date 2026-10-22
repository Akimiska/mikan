package tgbot

import (
	"context"
	"regexp"
	"strings"
)

// AoiVPN fork: the welcome's {trial_line}. The admin's welcome promised «3 дня бесплатно» to
// everyone, while the trial's button shows only to an account that may take it (never a
// client, no trial taken): a customer read the promise and found no button. The line is
// filled only alongside the button, with the trial tariff's days; elsewhere it is empty and
// the blank lines it leaves are closed up.

func aoiTrialLine(ctx context.Context, shop Shop, w *words, shown bool) string {
	if !shown {
		return ""
	}
	days := "3 дня"
	if t, ok := shop.TrialTariff(ctx); ok && t.DurationDays > 0 {
		days = w.days(int(t.DurationDays))
	}
	if w == &en {
		return "🎁 New users get " + days + " free."
	}
	return "🎁 Новым пользователям — " + days + " бесплатно."
}

var aoiBlankLines = regexp.MustCompile(`\n{3,}`)

// aoiWelcome closes up what an empty {trial_line} left: no run of blank lines, no trailing one.
func aoiWelcome(text string) string {
	return strings.TrimRight(aoiBlankLines.ReplaceAllString(text, "\n\n"), "\n")
}
