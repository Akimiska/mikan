package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/tgbot"
)

// TelegramView is the bot as the admin panel shows it. The token itself never leaves
// the panel.
type TelegramView struct {
	Enabled    bool               `json:"enabled"`
	TokenSet   bool               `json:"token_set" doc:"Токен сохранён"`
	TokenHint  string             `json:"token_hint,omitempty" doc:"ID бота из токена"`
	Running    bool               `json:"running"`
	Error      string             `json:"error,omitempty" doc:"token_invalid, token_revoked, unreachable или ответ Telegram"`
	Bot        *TelegramBot       `json:"bot,omitempty"`
	Config     tgbot.Config       `json:"config"`
	Defaults   tgbot.Texts        `json:"defaults" doc:"Встроенные тексты на языке бота: пустое поле берёт их"`
	MiniAppURL string             `json:"mini_app_url" doc:"Адрес Mini App; пусто — Telegram его не откроет: нет адреса или сертификат самоподписанный"`
	Linked     int64              `json:"linked" doc:"Подписок, привязанных к Telegram"`
	Accounts   int64              `json:"accounts" doc:"Аккаунтов Telegram с подписками"`
	Broadcast  *TelegramBroadcast `json:"broadcast,omitempty" doc:"Последняя рассылка с запуска панели"`
	Route      TelegramRoute      `json:"route" doc:"Как бот ходит в Telegram"`
}

type TelegramRoute struct {
	Mode   string `json:"mode" enum:"direct,node,proxy" doc:"Напрямую с сервера панели, через её ноду или через прокси — когда Telegram на сервере заблокирован"`
	NodeID int64  `json:"node_id,omitempty" doc:"Нода, через которую идут запросы"`
	Proxy  string `json:"proxy,omitempty" doc:"Адрес прокси; пароль скрыт"`
}

// TelegramBroadcast is how far the last broadcast went.
type TelegramBroadcast struct {
	Total   int   `json:"total"`
	Sent    int   `json:"sent"`
	Failed  int   `json:"failed" doc:"Не дошло: бот заблокирован, чат удалён"`
	Started int64 `json:"started" doc:"Unix-время начала"`
	Active  bool  `json:"active" doc:"Ещё отправляется"`
}

type TelegramBot struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type telegramOutput struct{ Body TelegramView }

type patchTelegramInput struct {
	Body struct {
		Enabled *bool         `json:"enabled,omitempty"`
		Token   *string       `json:"token,omitempty" maxLength:"100" doc:"Токен от @BotFather; пустая строка — удалить"`
		Config  *tgbot.Config `json:"config,omitempty"`
		Route   *struct {
			Mode   string  `json:"mode" enum:"direct,node,proxy"`
			NodeID int64   `json:"node_id,omitempty" minimum:"0" doc:"Удалённая нода панели (mode=node)"`
			Proxy  *string `json:"proxy,omitempty" maxLength:"512" doc:"socks5://user:pass@host:port, http://… или https://…; не передан — прежний (mode=proxy)"`
		} `json:"route,omitempty" doc:"Перед сохранением панель проверяет, что Telegram отвечает этим путём"`
	}
}

type broadcastInput struct {
	Body struct {
		Text string `json:"text" minLength:"1" maxLength:"3500" doc:"Обычный текст; {brand} — название сервиса"`
	}
}

type broadcastOutput struct {
	Body struct {
		Queued int `json:"queued"`
	}
}

func (h *handlers) registerTelegram() {
	tags := []string{"telegram"}
	huma.Register(h.api, huma.Operation{OperationID: "get-telegram", Method: http.MethodGet, Path: "/api/v1/telegram", Summary: "Telegram-бот", Tags: tags}, h.getTelegram)
	huma.Register(h.api, huma.Operation{OperationID: "update-telegram", Method: http.MethodPatch, Path: "/api/v1/telegram", Summary: "Настроить Telegram-бота", Tags: tags}, h.updateTelegram)
	huma.Register(h.api, huma.Operation{OperationID: "telegram-broadcast", Method: http.MethodPost, Path: "/api/v1/telegram/broadcast", Summary: "Разослать сообщение всем в боте", Tags: tags, DefaultStatus: http.StatusAccepted}, h.broadcast)
	huma.Register(h.api, huma.Operation{OperationID: "unlink-telegram", Method: http.MethodDelete, Path: "/api/v1/users/{id}/telegram", Summary: "Отвязать подписку от Telegram", Tags: tags, DefaultStatus: http.StatusNoContent}, h.unlinkTelegram)
}

func (h *handlers) telegramView(ctx context.Context) (TelegramView, error) {
	var v TelegramView
	var err error
	if v.Enabled, err = h.d.Settings.Bool(ctx, tgbot.KeyEnabled, false); err != nil {
		return v, err
	}
	token, err := h.d.Settings.String(ctx, tgbot.KeyToken)
	if err != nil {
		return v, err
	}
	if token != "" {
		v.TokenSet = true
		if id, _, ok := cutToken(token); ok {
			v.TokenHint = id
		}
	}
	if bot, ok, _ := settings.Get[tgbot.User](ctx, h.d.Settings, tgbot.KeyBot); ok && token != "" {
		v.Bot = &TelegramBot{Username: bot.Username, Name: bot.FirstName}
	}
	if h.d.Telegram != nil {
		st := h.d.Telegram.Status()
		v.Running, v.Error = st.Running, st.Error
		v.Config = h.d.Telegram.Config(ctx)
		v.MiniAppURL = h.d.Telegram.MiniAppURL(ctx)
		if p := h.d.Telegram.Progress(); p.Total > 0 {
			v.Broadcast = &TelegramBroadcast{Total: p.Total, Sent: p.Sent, Failed: p.Failed, Started: p.Started.Unix(), Active: p.Active()}
		}
	} else {
		lang, err := h.d.Settings.Lang(ctx)
		if err != nil {
			return v, err
		}
		v.Config = tgbot.Default(lang)
	}
	v.Defaults = tgbot.DefaultTexts(v.Config.Lang)
	route := tgbot.Route{Mode: tgbot.RouteDirect}
	if h.d.Telegram != nil {
		route = h.d.Telegram.Route(ctx)
	}
	v.Route = TelegramRoute{Mode: route.Mode, NodeID: route.NodeID, Proxy: tgbot.MaskProxy(route.Proxy)}
	if v.Linked, err = h.d.Store.Q.CountTgLinks(ctx); err != nil {
		return v, err
	}
	if v.Accounts, err = h.d.Store.Q.CountTgChats(ctx); err != nil {
		return v, err
	}
	return v, nil
}

func (h *handlers) getTelegram(ctx context.Context, _ *struct{}) (*telegramOutput, error) {
	v, err := h.telegramView(ctx)
	if err != nil {
		return nil, err
	}
	return &telegramOutput{Body: v}, nil
}

var tokenRe = regexp.MustCompile(`^(\d{5,15}):([A-Za-z0-9_-]{30,50})$`)

// cutToken splits a bot token into the bot id and the secret part.
func cutToken(t string) (id, secret string, ok bool) {
	m := tokenRe.FindStringSubmatch(t)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func tgFieldErr(field, code string) error {
	return huma.Error422UnprocessableEntity(code, &huma.ErrorDetail{Location: "body." + field, Message: code})
}

func (h *handlers) updateTelegram(ctx context.Context, in *patchTelegramInput) (*telegramOutput, error) {
	if h.d.Telegram == nil {
		return nil, huma.Error503ServiceUnavailable("bot_unavailable")
	}
	b := in.Body
	details := map[string]any{}
	// The route goes first: a token typed together with it is checked the new way.
	if b.Route != nil {
		r := h.d.Telegram.Route(ctx)
		r.Mode = b.Route.Mode
		switch r.Mode {
		case tgbot.RouteNode:
			n, err := h.d.Store.Q.GetNode(ctx, b.Route.NodeID)
			if err != nil || n.Address == "" {
				// The panel's own node shares its server, and with it the block.
				return nil, tgFieldErr("route", "tg_route_node")
			}
			r.NodeID = n.ID
			details["route"], details["node"] = r.Mode, n.Name
		case tgbot.RouteProxy:
			if b.Route.Proxy != nil {
				r.Proxy = strings.TrimSpace(*b.Route.Proxy)
			}
			if _, err := tgbot.ParseProxy(r.Proxy); err != nil {
				return nil, tgFieldErr("route", "tg_proxy_invalid")
			}
			details["route"], details["proxy"] = r.Mode, tgbot.ProxyHost(r.Proxy)
		default:
			details["route"] = r.Mode
		}
		if r.Mode != tgbot.RouteDirect {
			token, err := h.d.Settings.String(ctx, tgbot.KeyToken)
			if err != nil {
				return nil, err
			}
			if b.Token != nil {
				if _, _, ok := cutToken(*b.Token); ok {
					token = *b.Token
				}
			}
			if err := h.d.Telegram.CheckRoute(ctx, r, token); err != nil {
				return nil, tgFieldErr("route", "tg_route_unreachable")
			}
		}
		if err := settings.Set(ctx, h.d.Settings, tgbot.KeyRoute, r); err != nil {
			return nil, err
		}
	}
	if b.Token != nil {
		switch {
		case *b.Token == "":
			if err := settings.Set(ctx, h.d.Settings, tgbot.KeyToken, ""); err != nil {
				return nil, err
			}
			if err := settings.Set(ctx, h.d.Settings, tgbot.KeyEnabled, false); err != nil {
				return nil, err
			}
			details["token"] = "removed"
		default:
			if _, _, ok := cutToken(*b.Token); !ok {
				return nil, tgFieldErr("token", "tg_token_format")
			}
			me, err := h.d.Telegram.CheckToken(ctx, *b.Token)
			var ae *tgbot.APIError
			switch {
			case errors.As(err, &ae) && (ae.Code == 401 || ae.Code == 404):
				return nil, tgFieldErr("token", "tg_token_invalid")
			case err != nil:
				return nil, huma.Error502BadGateway("tg_unreachable")
			}
			if err := settings.Set(ctx, h.d.Settings, tgbot.KeyToken, *b.Token); err != nil {
				return nil, err
			}
			if err := settings.Set(ctx, h.d.Settings, tgbot.KeyBot, me); err != nil {
				return nil, err
			}
			details["token"], details["bot"] = "set", me.Username
		}
	}
	if b.Config != nil {
		cfg := *b.Config
		if err := cfg.Validate(); err != nil {
			return nil, tgFieldErr("config", err.Error())
		}
		if err := settings.Set(ctx, h.d.Settings, tgbot.KeyConfig, cfg); err != nil {
			return nil, err
		}
		details["config"] = true
	}
	if b.Enabled != nil {
		token, err := h.d.Settings.String(ctx, tgbot.KeyToken)
		if err != nil {
			return nil, err
		}
		if *b.Enabled && token == "" {
			return nil, tgFieldErr("enabled", "tg_no_token")
		}
		if err := settings.Set(ctx, h.d.Settings, tgbot.KeyEnabled, *b.Enabled); err != nil {
			return nil, err
		}
		details["enabled"] = *b.Enabled
	}
	h.d.Telegram.Reload()
	h.audit(ctx, sessionOf(ctx).AdminID, "telegram.update", "telegram", "", details)
	v, err := h.telegramView(ctx)
	if err != nil {
		return nil, err
	}
	return &telegramOutput{Body: v}, nil
}

func (h *handlers) broadcast(ctx context.Context, in *broadcastInput) (*broadcastOutput, error) {
	if h.d.Telegram == nil {
		return nil, huma.Error503ServiceUnavailable("bot_unavailable")
	}
	n, err := h.d.Telegram.Broadcast(ctx, in.Body.Text)
	switch {
	case errors.Is(err, tgbot.ErrOff):
		return nil, huma.Error409Conflict("bot_off")
	case errors.Is(err, tgbot.ErrBusy):
		return nil, huma.Error409Conflict("broadcast_busy")
	case err != nil:
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "telegram.broadcast", "telegram", "", map[string]any{"recipients": n})
	out := &broadcastOutput{}
	out.Body.Queued = n
	return out, nil
}

func (h *handlers) unlinkTelegram(ctx context.Context, in *userIDInput) (*struct{}, error) {
	if _, err := h.d.Users.Get(ctx, in.ID); err != nil {
		return nil, mapDomainErr(err)
	}
	if err := h.d.Store.Q.UnlinkTg(ctx, in.ID); err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "user.unlink_telegram", "user", strconv.FormatInt(in.ID, 10), nil)
	return nil, nil
}
