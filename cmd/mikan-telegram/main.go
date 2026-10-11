// Command mikan-telegram is the official bot addon of mikan (getmikan/marketplace, id
// telegram): the subscribers' bot with the panel's own screens. It runs next to the panel
// as an addon of protocol 2 in client mode and sees the panel only through what the host
// hands it:
//
//	MIKAN_PANEL_URL, MIKAN_PANEL_TOKEN  the Panel API: the bot's data (POST /bot/{method})
//	                                     and the feed of events (payment messages, notices)
//	MIKAN_BOT_API_URL, MIKAN_BOT_TOKEN  the Bot API proxy: the updates and the sends; the
//	                                     panel keeps the real token and the payments
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/tgate"
	"mikan/internal/panel/tgbot"
)

// version is set at build time (-ldflags -X main.version=...).
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	env := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			log.Error("not set", "var", k)
			os.Exit(2)
		}
		return v
	}
	panelURL, panelToken := env("MIKAN_PANEL_URL"), env("MIKAN_PANEL_TOKEN")
	botURL, botToken := env("MIKAN_BOT_API_URL"), env("MIKAN_BOT_TOKEN")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("mikan-telegram starting", "version", version)
	a := newAddon(panelURL, panelToken, botURL, botToken, log)
	a.Run(ctx)
	log.Info("mikan-telegram stopped")
}

// addon is the bot's screens on the panel's data and the Bot API proxy.
type addon struct {
	log         *slog.Logger
	panelURL    string
	panelToken  string
	client      *tgate.Client
	out         *tgate.Outbox
	bot         *tgbot.Bot
	backend     *tgbot.Remote
	hc          *http.Client
	running     sync.WaitGroup
	updatesWait time.Duration
}

func newAddon(panelURL, panelToken, botURL, botToken string, log *slog.Logger) *addon {
	a := &addon{log: log, panelURL: strings.TrimRight(panelURL, "/"), panelToken: panelToken,
		hc: &http.Client{Timeout: 90 * time.Second}, updatesWait: 25 * time.Second}
	a.client = tgate.NewClient(botURL, botToken, nil)
	a.out = tgate.NewOutbox(a.client, tgate.DefaultLimits, time.Now, func(int64) {},
		func(err error) { log.Warn("send", "err", err) })
	a.backend = tgbot.NewRemote(panelURL, panelToken, nil)
	a.bot = tgbot.New(tgbot.Deps{Backend: a.backend, Gateway: a, Log: log, Now: time.Now})
	return a
}

// The Transport of the screens: the addon's own outbox to the proxy, the panel's channel check.
func (a *addon) Outbox() *tgate.Outbox { return a.out }
func (a *addon) Go(f func())           { a.running.Go(f) }
func (a *addon) Member(ctx context.Context, chat string, tgID int64, fresh bool) (bool, error) {
	return a.backend.Member(ctx, chat, tgID, fresh)
}

// Run takes the updates and the events until ctx ends, and returns when all it started has.
func (a *addon) Run(ctx context.Context) {
	defer a.running.Wait()
	a.running.Go(func() { a.out.Run(ctx) })
	a.bot.Started(ctx, a.client)
	a.running.Go(func() { a.events(ctx) })
	a.updates(ctx)
}

// updates reads the panel's buffer as Telegram's getUpdates: an update is acknowledged with
// the next call, after the screens took it.
func (a *addon) updates(ctx context.Context) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		ups, err := a.client.Updates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.log.Warn("updates", "err", err)
			sleep(ctx, backoff)
			backoff = min(2*backoff, time.Minute)
			continue
		}
		backoff = time.Second
		for _, up := range ups {
			if err := a.bot.Update(ctx, a.client, a.out, up); err != nil {
				a.log.Warn("update", "id", up.UpdateID, "err", err)
			}
			offset = up.UpdateID + 1
		}
	}
}

// events reads the feed of the events the bot acts on, and acknowledges what it handled.
func (a *addon) events(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		evs, err := a.pull(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.log.Warn("events", "err", err)
			sleep(ctx, backoff)
			backoff = min(2*backoff, time.Minute)
			continue
		}
		backoff = time.Second
		for _, e := range evs {
			if err := a.bot.HandleEvent(ctx, e); err != nil {
				// Not acknowledged: it comes again with the next pull.
				a.log.Warn("event", "id", e.ID, "type", e.Type, "err", err)
				sleep(ctx, 5*time.Second)
				break
			}
			if err := a.ack(ctx, e.ID); err != nil {
				a.log.Warn("ack", "id", e.ID, "err", err)
				break
			}
		}
	}
}

func (a *addon) pull(ctx context.Context) ([]events.Event, error) {
	url := fmt.Sprintf("%s/events?wait=%d&types=%s", a.panelURL, int(a.updatesWait/time.Second), strings.Join(tgbot.EventTypes, ","))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.panelToken)
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("events: HTTP %d", resp.StatusCode)
	}
	var feed struct {
		Events []events.Event `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}
	return feed.Events, nil
}

func (a *addon) ack(ctx context.Context, id int64) error {
	body, _ := json.Marshal(map[string]int64{"id": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.panelURL+"/events/ack", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.panelToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return errors.New("ack: HTTP " + resp.Status)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
