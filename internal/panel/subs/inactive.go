package subs

import (
	"mikan/internal/panel/domain"
)

// A subscription that cannot connect (its term over, its traffic used up, turned off) gets
// one "server" in place of the list when the admin turns it on (Config.InactiveServer): its
// name tells the subscriber what happened and what to do, so the app does not just fail on
// every server. The admin's own text (Config.InactiveText) takes the variables of the title.

// inactiveError is a subscription that cannot connect, with what its notice says.
type inactiveError struct{ text string }

func (e *inactiveError) Error() string { return "inactive: " + e.text }

// inactiveNotice is the notice for a subscription in state, or nil when it can connect or
// the notice is off.
func inactiveNotice(cfg Config, state string, vars map[string]string) error {
	if !cfg.InactiveServer || domain.CanConnect(state) {
		return nil
	}
	if cfg.InactiveText != "" {
		if s := fillTitle(cfg.InactiveText, vars); s != "" {
			return &inactiveError{text: s}
		}
	}
	return &inactiveError{text: inactiveDefault(state, cfg.Lang == "en")}
}

func inactiveDefault(state string, en bool) string {
	switch {
	case state == domain.StateLimited && en:
		return "⛔ The traffic is used up — buy more or renew in the bot"
	case state == domain.StateLimited:
		return "⛔ Трафик закончился — докупите или продлите подписку в боте"
	case state == domain.StateDisabled && en:
		return "⛔ The subscription is turned off — write to support"
	case state == domain.StateDisabled:
		return "⛔ Подписка отключена — напишите в поддержку"
	case en:
		return "⛔ The subscription has ended — renew it in the bot"
	}
	return "⛔ Подписка закончилась — продлите её в боте"
}
