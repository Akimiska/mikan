package tgbot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
)

// A notice goes once per subscription term or traffic period (tg_notices).
type notice struct {
	kind   string
	period int64
	text   string
}

// due are the notices a subscription has earned now; grants is what is left of its main
// traffic packages: traffic runs out only when they do too.
func due(u db.User, grants int64, now time.Time, n Notify) []notice {
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
		case domain.TrafficLeft(u.TrafficLimit, used, grants) == 0 && n.Traffic100:
			out = append(out, notice{kind: "traffic_100", period: u.PeriodStart})
		case grants <= 0 && used*10 >= u.TrafficLimit.Int64*9 && n.Traffic90:
			out = append(out, notice{kind: "traffic_90", period: u.PeriodStart})
		}
	}
	return out
}

// What a notice has been through in this process. tg_notices holds only the notices that
// were delivered, so a notice that was not (Telegram unreachable, the outbox stopped by a
// reload or a restart) is still due at the next round and goes again; this keeps one that
// is on its way, or has just failed, from being queued twice meanwhile.
type noticeState struct {
	queued  bool      // in the outbox
	retryAt time.Time // delivery failed: not again before this
	sent    bool      // delivered, but the row could not be written
	// known: the database says it was delivered; not asked again before this, so a user
	// who stays in a notice's window is not looked up every round.
	known time.Time
}

// noticeKnown is how long a delivered notice found in the database is remembered.
const noticeKnown = 24 * time.Hour

// noticeRetry is how long a notice waits after a failed delivery.
const noticeRetry = 5 * time.Minute

func noticeKey(userID int64, n notice) string {
	return fmt.Sprintf("%d/%s/%d", userID, n.kind, n.period)
}

// claimNotice says whether n goes out now: not delivered before, not on its way, not
// waiting out a failure.
func (b *Bot) claimNotice(ctx context.Context, userID int64, n notice, now time.Time) (string, bool) {
	key := noticeKey(userID, n)
	b.noticeMu.Lock()
	if st := b.notices[key]; st != nil && (st.queued || st.sent || now.Before(st.retryAt) || now.Before(st.known)) {
		b.noticeMu.Unlock()
		return "", false
	}
	// Taken before the database is asked, which is asked without the lock held.
	b.notices[key] = &noticeState{queued: true}
	b.noticeMu.Unlock()
	sent, err := b.d.Backend.NoticeSent(ctx, userID, n.kind, n.period)
	if err != nil || sent {
		if err != nil {
			b.d.Log.Error("telegram: notices", "err", err)
		}
		b.noticeMu.Lock()
		if sent {
			b.notices[key] = &noticeState{known: now.Add(noticeKnown)}
		} else {
			delete(b.notices, key)
		}
		b.noticeMu.Unlock()
		return "", false
	}
	return key, true
}

// noticeDone is the outbox's word on a notice. Delivered, or refused for good (the user
// blocked the bot, the chat is gone: sending again changes nothing), it is recorded and
// never comes again. Anything else leaves it due for the next round.
func (b *Bot) noticeDone(userID int64, n notice, key string, err error) {
	var ae *tgate.APIError
	// A refusal below 500 is Telegram's final word; a failure on its side is not.
	delivered := err == nil || errors.As(err, &ae) && ae.Code < 500
	if !delivered {
		b.noticeMu.Lock()
		defer b.noticeMu.Unlock()
		if st := b.notices[key]; st != nil {
			if errors.Is(err, context.Canceled) {
				delete(b.notices, key) // the bot was stopped: the next round after it starts sends it
				return
			}
			st.queued, st.retryAt = false, b.d.Now().Add(noticeRetry)
		}
		return
	}
	// Written without the lock held; the notice stays queued meanwhile, so it is not claimed.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	werr := b.d.Backend.NoticeDelivered(ctx, userID, n.kind, n.period, b.d.Now())
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	st := b.notices[key]
	if st == nil {
		return
	}
	if werr != nil {
		b.d.Log.Error("telegram: notice sent, but not recorded", "err", werr)
		st.queued, st.sent = false, true
		return
	}
	delete(b.notices, key)
}

// forgetNotices drops what is no longer of use: failures that have waited long enough.
func (b *Bot) forgetNotices(now time.Time) {
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	for k, st := range b.notices {
		if !st.queued && !st.sent && !now.Before(st.retryAt) && !now.Before(st.known) {
			delete(b.notices, k)
		}
	}
}

// night: automatic notices then come without a sound. Most users live on Moscow time;
// a notice at 3 a.m. with a ring is how a bot gets reported as spam.
func night(t time.Time) bool {
	h := t.In(time.FixedZone("MSK", 3*60*60)).Hour()
	return h >= 22 || h < 9
}

// Notify queues the notices that are due, each until it is delivered, and not twice at
// once. The outbox paces them behind the replies to people in their chats.
func (b *Bot) Notify(ctx context.Context) {
	if b.off(ctx) {
		b.emitNotices(ctx)
		return
	}
	out := b.d.Gateway.Outbox()
	if out == nil {
		return
	}
	cfg := b.Config(ctx)
	w := wordsFor(cfg.Lang)
	now := b.d.Now()
	silent := cfg.QuietNight && night(now)
	b.forgetNotices(now)
	subs, err := b.d.Backend.Notices(ctx, now)
	if err != nil {
		b.d.Log.Error("telegram: notices", "err", err)
		return
	}
	for _, l := range subs {
		u := l.User
		for _, n := range due(u, l.GrantsLeft, now, cfg.Notify) {
			key, ok := b.claimNotice(ctx, u.ID, n, now)
			if !ok {
				continue
			}
			vars := b.vars(ctx, w, u, now)
			text := render(map[string]string{"expire_3d": pick(cfg.Texts.Expiring, w.expiring), "expire_1d": pick(cfg.Texts.Expiring, w.expiring),
				"expired": pick(cfg.Texts.Expired, w.expired), "traffic_90": pick(cfg.Texts.Traffic90, w.traffic90), "traffic_100": pick(cfg.Texts.TrafficEnd, w.trafficEnd)}[n.kind], vars)
			var rows [][]tgate.Button
			// Traffic running out: the packages there are for this subscription, a tap away.
			if n.kind == "traffic_90" || n.kind == "traffic_100" {
				if len(b.packageOffers(ctx, u.ID)) > 0 {
					rows = append(rows, []tgate.Button{{Text: w.buyTraffic, CallbackData: "xu:" + strconv.FormatInt(u.ID, 10)}})
				}
			}
			if n.kind != "traffic_90" {
				rows = append(rows, []tgate.Button{{Text: labelOf(cfg, "renew", "💳"), CallbackData: "r"}})
			}
			var kb *tgate.Keyboard
			if len(rows) > 0 {
				kb = &tgate.Keyboard{InlineKeyboard: rows}
			}
			chat, userID := l.TgID, u.ID
			out.Notice(chat, func(ctx context.Context, c *tgate.Client) error {
				_, err := c.Send(ctx, chat, text, kb, silent)
				return err
			}, func(err error) { b.noticeDone(userID, n, key, err) })
		}
	}
	if now.Hour() == 3 && now.Minute() < 10 {
		if err := b.d.Backend.PruneNotices(ctx, now.Add(-90*24*time.Hour)); err != nil {
			b.d.Log.Error("telegram: prune notices", "err", err)
		}
	}
}

// ErrBusy: a broadcast is being sent; one at a time.
var ErrBusy = errors.New("broadcast_busy")

// Broadcast queues the admin's text for every account with a subscription and returns
// how many will get it. The outbox sends it at 20 messages a second at most, after the
// replies and the notices; the admin panel shows the progress.
func (b *Bot) Broadcast(ctx context.Context, text string) (int, error) {
	out := b.d.Gateway.Outbox()
	if out == nil {
		return 0, tgate.ErrOff
	}
	b.mu.Lock()
	if b.bcast.Active() {
		b.mu.Unlock()
		return 0, ErrBusy
	}
	targets, err := b.d.Backend.BroadcastTargets(ctx)
	if err != nil {
		b.mu.Unlock()
		return 0, err
	}
	b.bcast = BroadcastProgress{Total: len(targets), Started: b.d.Now()}
	b.mu.Unlock()
	body := render(text, map[string]string{"brand": b.brand(ctx)})
	queue := func(chat int64) {
		out.Bulk(chat, func(ctx context.Context, c *tgate.Client) error {
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
	// The outbox sends 20 a second; queueing everyone at once would keep a message and a
	// closure per account in memory for as long as that takes. A first batch goes in now,
	// the rest as the lane drains.
	first := min(len(targets), bulkBatch)
	for _, chat := range targets[:first] {
		queue(chat)
	}
	if rest := targets[first:]; len(rest) > 0 {
		go b.feed(out, rest, queue)
	}
	return len(targets), nil
}

// bulkBatch is how many broadcast messages wait in the outbox; feed tops the lane up when
// fewer than bulkLow are left.
const (
	bulkBatch = 500
	bulkLow   = 100
)

// feed queues the rest of a broadcast in batches as the outbox sends. If the outbox stops
// (the bot is reloaded or switched off) what is left is counted as failed, so the
// progress ends.
func (b *Bot) feed(out *tgate.Outbox, rest []int64, queue func(chat int64)) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for len(rest) > 0 {
		select {
		case <-out.Stopped():
			b.mu.Lock()
			b.bcast.Failed += len(rest)
			b.mu.Unlock()
			return
		case <-t.C:
		}
		if out.BulkPending() > bulkLow {
			continue
		}
		n := min(len(rest), bulkBatch)
		for _, chat := range rest[:n] {
			queue(chat)
		}
		rest = rest[n:]
	}
}

// Progress is the last broadcast.
func (b *Bot) Progress() BroadcastProgress {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bcast
}

// Events of the notices, for an addon that holds the bot.
const (
	EventExpiring   = "user.expiring"
	EventExpired    = "user.expired"
	EventTraffic90  = "traffic.90"
	EventTraffic100 = "traffic.100"
)

// NoticeEvent is what the addon is told of a notice: which subscription, and what is due.
type NoticeEvent struct {
	Days       int   `json:"days,omitempty"`        // user.expiring: 3 or 1
	ExpiresAt  int64 `json:"expires_at,omitempty"`  // unix seconds
	PeriodFrom int64 `json:"period_from,omitempty"` // traffic: the period it is about
}

// emitNotices is Notify while an addon holds the bot: the same notices, by the admin's
// switches and once each (tg_notices), as events for the addon to word and send.
func (b *Bot) emitNotices(ctx context.Context) {
	if b.d.Emit == nil {
		return
	}
	cfg := b.Config(ctx)
	now := b.d.Now()
	subs, err := b.d.Backend.Notices(ctx, now)
	if err != nil {
		b.d.Log.Error("telegram: notices", "err", err)
		return
	}
	for _, l := range subs {
		u := l.User
		for _, n := range due(u, l.GrantsLeft, now, cfg.Notify) {
			sent, err := b.d.Backend.NoticeSent(ctx, u.ID, n.kind, n.period)
			if err != nil || sent {
				continue
			}
			typ, data := EventExpiring, NoticeEvent{ExpiresAt: u.ExpiresAt.Int64}
			switch n.kind {
			case "expire_3d":
				data.Days = 3
			case "expire_1d":
				data.Days = 1
			case "expired":
				typ = EventExpired
			case "traffic_90":
				typ, data = EventTraffic90, NoticeEvent{PeriodFrom: n.period}
			case "traffic_100":
				typ, data = EventTraffic100, NoticeEvent{PeriodFrom: n.period}
			}
			dedupe := "notice:" + n.kind + ":" + strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(n.period, 10)
			if err := b.d.Emit(ctx, typ, u.ID, l.TgID, data, dedupe); err != nil {
				b.d.Log.Error("telegram: notice event", "user", u.ID, "err", err)
				continue
			}
			if err := b.d.Backend.NoticeDelivered(ctx, u.ID, n.kind, n.period, now); err != nil {
				b.d.Log.Error("telegram: notices", "err", err)
			}
		}
	}
}
