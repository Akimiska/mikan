package app

import (
	"context"
	"fmt"
	"html"
	"strconv"

	"mikan/internal/panel/store/db"
)

// tellAdminOfTopup shows the admin, in the chat connected for the panel's alerts, a top-up
// that waits for them: the screenshot, who and how much. The decision is in the panel
// (Payments → Top-ups). Without the bot or the chat nobody is told; the page has it anyway.
func (p *Panel) tellAdminOfTopup(ctx context.Context, t db.Topup) {
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
			return
		}
		who := "tg " + strconv.FormatInt(t.TgID, 10)
		if c, err := p.st.Q.GetTgChat(ctx, t.TgID); err == nil && c.Username != "" {
			who = "@" + c.Username
		}
		amount := fmt.Sprintf("%d.%02d %s", t.Amount/100, t.Amount%100, t.Currency)
		text := fmt.Sprintf("💰 <b>Top-up waits</b>\n%s · %s\nDecide it: Payments, Top-ups.", html.EscapeString(who), amount)
		if g.Lang(ctx) != "en" {
			text = fmt.Sprintf("💰 <b>Заявка на пополнение</b>\n%s · %s\nРешить: Платежи, Пополнения.", html.EscapeString(who), amount)
		}
		// The screenshot is the bot's file: the infrastructure bot is another, so it may not
		// be able to send it; the text goes alone then.
		if _, err := client.SendPhoto(ctx, chat, t.Photo, text); err != nil {
			if _, err := client.Send(ctx, chat, text, nil, false); err != nil {
				p.log.Warn("topup notice: not delivered", "err", err)
			}
		}
	})
}
