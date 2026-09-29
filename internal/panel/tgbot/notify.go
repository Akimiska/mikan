package tgbot

import (
	"context"
	"errors"
	"time"

	"mikan/internal/panel/store/db"
)

// A notice goes once per subscription term or traffic period (tg_notices).
type notice struct {
	kind   string
	period int64
	text   string
}

// due are the notices a subscription has earned now.
func due(u db.User, now time.Time, n Notify) []notice {
	if u.Status == "disabled" {
		return nil
	}
	var out []notice
	if u.ExpiresAt.Valid {
		exp := time.Unix(u.ExpiresAt.Int64, 0)
		left := exp.Sub(now)
		switch {
		case left <= 0 && left > -3*24*time.Hour && n.Expired:
			out = append(out, notice{kind: "expired", period: u.ExpiresAt.Int64})
		case left > 0 && left <= 24*time.Hour && n.Expire1d:
			out = append(out, notice{kind: "expire_1d", period: u.ExpiresAt.Int64})
		case left > 24*time.Hour && left <= 3*24*time.Hour && n.Expire3d:
			out = append(out, notice{kind: "expire_3d", period: u.ExpiresAt.Int64})
		}
	}
	if u.TrafficLimit.Valid && u.TrafficLimit.Int64 > 0 {
		used := u.UsedUp + u.UsedDown
		switch {
		case used >= u.TrafficLimit.Int64 && n.Traffic100:
			out = append(out, notice{kind: "traffic_100", period: u.PeriodStart})
		case used*10 >= u.TrafficLimit.Int64*9 && n.Traffic90:
			out = append(out, notice{kind: "traffic_90", period: u.PeriodStart})
		}
	}
	return out
}

// night: automatic notices then come without a sound. Most users live on Moscow time;
// a notice at 3 a.m. with a ring is how a bot gets reported as spam.
func night(t time.Time) bool {
	h := t.In(time.FixedZone("MSK", 3*60*60)).Hour()
	return h >= 22 || h < 9
}

// Notify queues the notices that are due, each once. The outbox paces them behind the
// replies to people in their chats.
func (b *Bot) Notify(ctx context.Context) {
	out := b.out.Load()
	if out == nil {
		return
	}
	cfg := b.Config(ctx)
	w := wordsFor(cfg.Lang)
	now := b.d.Now()
	silent := cfg.QuietNight && night(now)
	links, err := b.d.Store.Q.ListTgLinks(ctx)
	if err != nil {
		b.d.Log.Error("telegram: notices", "err", err)
		return
	}
	for _, l := range links {
		if l.Blocked.Valid && l.Blocked.Int64 != 0 {
			continue
		}
		u, err := b.d.Store.Q.GetUser(ctx, l.UserID)
		if err != nil {
			continue
		}
		for _, n := range due(u, now, cfg.Notify) {
			added, err := b.d.Store.Q.AddTgNotice(ctx, db.AddTgNoticeParams{UserID: u.ID, Kind: n.kind, Period: n.period, SentAt: now.Unix()})
			if err != nil || added == 0 {
				continue
			}
			vars := b.vars(ctx, w, u, now)
			text := render(map[string]string{"expire_3d": pick(cfg.Texts.Expiring, w.expiring), "expire_1d": pick(cfg.Texts.Expiring, w.expiring),
				"expired": pick(cfg.Texts.Expired, w.expired), "traffic_90": pick(cfg.Texts.Traffic90, w.traffic90), "traffic_100": pick(cfg.Texts.TrafficEnd, w.trafficEnd)}[n.kind], vars)
			var kb *Keyboard
			if n.kind != "traffic_90" {
				kb = &Keyboard{[][]Button{{{Text: labelOf(cfg, "renew", "💳"), CallbackData: "r"}}}}
			}
			chat := l.TgID
			out.Notice(chat, func(ctx context.Context, c *Client) error {
				_, err := c.Send(ctx, chat, text, kb, silent)
				return err
			}, nil)
		}
	}
	if now.Hour() == 3 && now.Minute() < 10 {
		_ = b.d.Store.Q.PruneTgNotices(ctx, now.Add(-90*24*time.Hour).Unix())
	}
}

var (
	ErrBusy = errors.New("broadcast_busy")
	ErrOff  = errors.New("bot_off")
)

// Broadcast queues the admin's text for every account with a subscription and returns
// how many will get it. The outbox sends it at 20 messages a second at most, after the
// replies and the notices; the admin panel shows the progress.
func (b *Bot) Broadcast(ctx context.Context, text string) (int, error) {
	out := b.out.Load()
	if out == nil {
		return 0, ErrOff
	}
	b.mu.Lock()
	if b.bcast.Active() {
		b.mu.Unlock()
		return 0, ErrBusy
	}
	targets, err := b.d.Store.Q.BroadcastTargets(ctx)
	if err != nil {
		b.mu.Unlock()
		return 0, err
	}
	b.bcast = BroadcastProgress{Total: len(targets), Started: b.d.Now()}
	b.mu.Unlock()
	body := render(text, map[string]string{"brand": b.brand(ctx)})
	for _, chat := range targets {
		out.Bulk(chat, func(ctx context.Context, c *Client) error {
			_, err := c.Send(ctx, chat, body, nil, false)
			return err
		}, func(err error) {
			b.mu.Lock()
			if err == nil {
				b.bcast.Sent++
			} else {
				b.bcast.Failed++
			}
			b.mu.Unlock()
		})
	}
	return len(targets), nil
}

// Progress is the last broadcast.
func (b *Bot) Progress() BroadcastProgress {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bcast
}

// MiniAppUser checks the Mini App's initData and returns the subscriptions of that
// Telegram account.
func (b *Bot) MiniAppUser(ctx context.Context, initData string) ([]db.User, error) {
	token, err := b.d.Settings.String(ctx, KeyToken)
	if err != nil || token == "" {
		return nil, ErrOff
	}
	tu, err := CheckInitData(token, initData, b.d.Now())
	if err != nil {
		return nil, err
	}
	list, err := b.d.Store.Q.ListTgLinksOf(ctx, tu.ID)
	if err != nil {
		return nil, err
	}
	if ch, err := b.d.Store.Q.GetTgChat(ctx, tu.ID); err == nil {
		for i, u := range list {
			if u.ID == ch.Current && i > 0 {
				list[0], list[i] = list[i], list[0]
			}
		}
	}
	return list, nil
}
