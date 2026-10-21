package app

import (
	"context"
	"fmt"
	"html"
	"time"

	"mikan/internal/panel/support"
	"mikan/internal/textutil"
)

// supportNoticeTimeout bounds one message to the admin: a slow Telegram holds nothing else.
const supportNoticeTimeout = 15 * time.Second

// tellAdminOfSupport tells the admin, in the chat connected for the panel's alerts, that a
// support operator came in or that the access ended. The alerts' queue takes it (Notify): kept
// in the database and tried again for a day, since the admin learns of an operator's entry
// and of a terminal only through that chat. Without the bot or the chat nothing goes: the
// journal has the lines anyway.
func (p *Panel) tellAdminOfSupport(ctx context.Context, n support.Notice) {
	if p.Gateway == nil || p.Alerts == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	p.Alerts.Notify(ctx, "", supportNoticeText(p.Gateway.Lang(ctx), n))
}

// terminalNoticeText tells the admin that support opened a terminal on a server, or closed
// it: the server, how it ended and for how long. The recording is in Settings, Diagnostics.
func terminalNoticeText(ru bool, n support.Notice) string {
	server := html.EscapeString(n.Terminal)
	if !n.Leave {
		if ru {
			return fmt.Sprintf("🖥 <b>Поддержка открыла терминал</b>\nСервер: %s\nСеанс записывается. Отозвать: Настройки, Поддержка.", server)
		}
		return fmt.Sprintf("🖥 <b>Support opened a terminal</b>\nServer: %s\nThe session is recorded. Revoke it: Settings, Support.", server)
	}
	reason := map[bool]map[string]string{
		true: {"closed": "терминал закрыт", "idle": "15 минут без действий", "expired": "срок доступа истёк", "revoked": "доступ отозван", "exit": "оболочка завершилась",
			"recording": "запись переполнена", "error": "ошибка"},
		false: {"closed": "the terminal was closed", "idle": "15 minutes without a keystroke", "expired": "the access ran out", "revoked": "the access was revoked", "exit": "the shell ended",
			"recording": "the recording is full", "error": "an error"},
	}[ru][n.Reason]
	if reason == "" {
		reason = html.EscapeString(n.Reason)
	}
	if ru {
		return fmt.Sprintf("🖥 <b>Терминал поддержки закрыт</b>\nСервер: %s\nПричина: %s, длился %d с\nЗапись: Настройки, Диагностика.", server, reason, n.Seconds)
	}
	return fmt.Sprintf("🖥 <b>Support terminal closed</b>\nServer: %s\nReason: %s, lasted %d s\nRecording: Settings, Diagnostics.", server, reason, n.Seconds)
}

// supportNoticeText is the message in Telegram HTML. It names the level, the time and the
// admin's own note; never a token.
func supportNoticeText(lang string, n support.Notice) string {
	ru := !textutil.Lang(lang).En()
	if n.Terminal != "" {
		return terminalNoticeText(ru, n)
	}
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
			return fmt.Sprintf("🔧 <b>Поддержка вошла в панель</b>\nУровень: %s, доступ до %s%s\nОтозвать: Настройки, Поддержка.", level, until, note)
		}
		return fmt.Sprintf("🔧 <b>Support entered the panel</b>\nLevel: %s, access until %s%s\nRevoke it: Settings, Support.", level, until, note)
	}
	reason := map[bool]map[string]string{
		true:  {"revoked": "отозван", "replaced": "заменён новым", "expired": "срок истёк", "disabled": "доступ поддержки выключен", "left": "оператор закончил работу и вышел"},
		false: {"revoked": "revoked", "replaced": "replaced by a new one", "expired": "time ran out", "disabled": "support access was turned off", "left": "the operator finished and left"},
	}[ru][n.Reason]
	if ru {
		return fmt.Sprintf("🔧 <b>Доступ для поддержки закрыт</b>\nПричина: %s%s", reason, note)
	}
	return fmt.Sprintf("🔧 <b>Support access ended</b>\nReason: %s%s", reason, note)
}
