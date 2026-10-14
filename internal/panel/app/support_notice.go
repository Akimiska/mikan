package app

import (
	"context"
	"fmt"
	"html"
	"time"

	"mikan/internal/panel/support"
)

// supportNoticeTimeout bounds one message to the admin: a slow Telegram holds nothing else.
const supportNoticeTimeout = 15 * time.Second

// tellAdminOfSupport tells the admin, in the chat connected for the panel's alerts, that a
// support operator came in or that the access ended. It does nothing without the bot or the
// chat: the journal has the lines anyway. The message goes out on the gateway's own
// goroutine, so the request that caused it does not wait for Telegram.
func (p *Panel) tellAdminOfSupport(ctx context.Context, n support.Notice) {
	g := p.Gateway
	if g == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	g.Go(func() {
		ctx, cancel := context.WithTimeout(ctx, supportNoticeTimeout)
		defer cancel()
		if !g.InfrastructureEnabled(ctx) {
			return
		}
		chat, ok, err := g.InfrastructureAdminChat(ctx)
		if err != nil || !ok {
			return
		}
		client, err := g.InfrastructureClient(ctx)
		if err != nil {
			p.log.Warn("support notice: no bot client", "err", err)
			return
		}
		if _, err := client.Send(ctx, chat, supportNoticeText(g.Lang(ctx), n), nil, false); err != nil {
			p.log.Warn("support notice: not delivered", "err", err)
		}
	})
}

// supportNoticeText is the message in Telegram HTML. It names the level, the time and the
// admin's own note; never a token.
func supportNoticeText(lang string, n support.Notice) string {
	ru := lang != "en"
	level := map[bool]map[string]string{
		true:  {support.LevelView: "просмотр", support.LevelChange: "изменение", support.LevelTerminal: "терминал"},
		false: {support.LevelView: "view", support.LevelChange: "change", support.LevelTerminal: "terminal"},
	}[ru][n.Grant.Level]
	until := support.Expires(n.Grant).Format("15:04") + " UTC"
	note := ""
	if n.Grant.Note != "" {
		note = "\n" + html.EscapeString(n.Grant.Note)
	}
	if !n.Leave {
		if ru {
			return fmt.Sprintf("🔧 <b>Поддержка вошла в панель</b>\nУровень: %s, доступ до %s%s\nОтозвать: Настройки, Безопасность, Доступ для поддержки.", level, until, note)
		}
		return fmt.Sprintf("🔧 <b>Support entered the panel</b>\nLevel: %s, access until %s%s\nRevoke it: Settings, Security, Support access.", level, until, note)
	}
	reason := map[bool]map[string]string{
		true:  {"revoked": "отозван", "replaced": "заменён новым", "expired": "срок истёк"},
		false: {"revoked": "revoked", "replaced": "replaced by a new one", "expired": "time ran out"},
	}[ru][n.Reason]
	if ru {
		return fmt.Sprintf("🔧 <b>Доступ для поддержки закрыт</b>\nПричина: %s%s", reason, note)
	}
	return fmt.Sprintf("🔧 <b>Support access ended</b>\nReason: %s%s", reason, note)
}
