package panelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// What the bot addon may do over the bot's RPC (botpanel.RPCHandler) beyond holding the bot:
// each method needs the permission its effect asks for, and acts only for Telegram accounts
// that are talking to the bot now. An addon cannot read or move the subscription of
// somebody who never wrote to it, whatever ids it makes up.

// rpcRule is one method's: the permission it needs, the arguments (by position) that are
// Telegram accounts which must be talking to the bot, those that are subscriptions whose
// owner must be, and those that are subscriptions the panel named to the addon lately by a
// code or an address someone sent the bot (UserOfCode, UserOfAddress). tgField names the
// account inside a request object (Shop.Invoice).
type rpcRule struct {
	perm    string
	tg      []int
	owner   []int
	named   []int
	tgField int // argument holding an object with TgID; -1: none
}

func rule(perm string, tg, owner []int) rpcRule {
	return rpcRule{perm: perm, tg: tg, owner: owner, tgField: -1}
}

var botRPC = map[string]rpcRule{
	// The bot's own setup: no one's data.
	"Config": rule("", nil, nil), "Brand": rule("", nil, nil), "SupportURL": rule("", nil, nil),
	"MiniAppURL": rule("", nil, nil), "DeviceBinding": rule("", nil, nil), "Tariff": rule("", nil, nil),
	"Member": rule("", []int{1}, nil),
	// Accounts and their subscriptions.
	"Context":       rule("users:read", []int{0}, nil),
	"Chat":          rule("users:read", []int{0}, nil),
	"UserOfCode":    rule("users:read", nil, nil),
	"UserOfAddress": rule("users:read", nil, nil),
	"Owner":         rule("users:read", nil, []int{0}),
	"SubURL":        rule("users:read", nil, nil),
	"User":          rule("users:read", nil, []int{0}),
	"GrantsLeft":    rule("users:read", nil, []int{0}),
	"Pools":         rule("users:read", nil, []int{0}),
	"DeviceCount":   rule("users:read", nil, []int{0}),
	"Devices":       rule("users:read", nil, []int{0}),
	"DeviceTraffic": rule("users:read", nil, []int{0}),
	"UnbindState":   rule("users:read", nil, []int{0}),
	"ReissueState":  rule("users:read", nil, []int{0}),
	"SetMenu":       rule("users:write", []int{0}, nil),
	"SetCurrent":    rule("users:write", []int{0}, nil),
	// A subscription is linked to the account that sent the bot its code or its address: an
	// id the addon made up, of a subscription nobody claimed, is not.
	"Link":        {perm: "users:write", tg: []int{0}, named: []int{1}, tgField: -1},
	"Transfer":    rule("users:write", []int{1}, []int{0}),
	"Unlink":      rule("users:write", nil, []int{0}),
	"Unbind":      rule("users:write", nil, []int{0}),
	"Reissue":     rule("users:write", nil, []int{0}),
	"TagCampaign": rule("users:write", []int{0}, nil),
	// The shop: what is on offer, and what makes a payment or a subscription.
	"Shop.Config": rule("", nil, nil), "Shop.Offers": rule("", nil, nil), "Shop.AddonName": rule("", nil, nil),
	"Shop.TrialTariff":    rule("", nil, nil),
	"Shop.PackageOffers":  rule("", nil, []int{0}),
	"Shop.TrialOpen":      rule("", []int{0}, nil),
	"Shop.TrialAllowed":   rule("", []int{0}, nil),
	"Shop.TrialOffer":     rule("", []int{0}, nil),
	"Shop.FreeOffer":      rule("", []int{0}, nil),
	"Shop.Invoice":        {perm: "billing:invoice", tgField: 0},
	"Shop.PackageInvoice": {perm: "billing:invoice", tgField: 0},
	"Shop.DeviceOfferFor": rule("", nil, []int{0}),
	"Shop.DeviceInvoice":  {perm: "billing:invoice", tgField: 0},
	"Shop.TrialInvoice":   rule("billing:invoice", []int{0}, nil),
	"Shop.Trial":          rule("billing:invoice", []int{0}, nil),
	"Shop.FreePlan":       rule("billing:invoice", []int{0}, nil),
	"Shop.BalanceOf":      rule("users:read", []int{0}, nil),
	"Shop.BalanceHistory": rule("users:read", []int{0}, nil),
	"Shop.TopupDraft":     rule("users:read", []int{0}, nil),
	"Shop.StartTopup":     rule("billing:invoice", []int{0}, nil),
	"Shop.SetTopupAmount": rule("billing:invoice", []int{0}, nil),
	"Shop.CancelTopup":    rule("billing:invoice", []int{0}, nil),
	"Shop.SubmitTopup":    rule("billing:invoice", []int{0}, nil),
}

// talkWindow: how long an account counts as talking to the bot after its last message.
const talkWindow = 30 * time.Minute

// talking keeps the accounts the bot addon got updates or events about lately, and the
// subscriptions the panel named to it by a code or an address (named).
type talking struct {
	mu    sync.Mutex
	seen  map[int64]time.Time
	named map[int64]time.Time
}

func (t *talking) mark(tg int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	note(&t.seen, tg)
}

// name notes a subscription the panel named to the addon.
func (t *talking) name(user int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	note(&t.named, user)
}

// note keeps id in m as seen now; past a size, what is older than the window goes.
func note(m *map[int64]time.Time, id int64) {
	if id == 0 {
		return
	}
	now := time.Now()
	if *m == nil {
		*m = map[int64]time.Time{}
	}
	(*m)[id] = now
	if len(*m) > 50000 {
		for k, at := range *m {
			if now.Sub(at) > talkWindow {
				delete(*m, k)
			}
		}
	}
}

func (t *talking) recent(tg int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.seen[tg]
	return ok && time.Since(at) <= talkWindow
}

func (t *talking) wasNamed(user int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.named[user]
	return ok && time.Since(at) <= talkWindow
}

// noteNamed reads the answer of a call that names a subscription by a code or an address
// ({"results":[id, ...]}) and keeps the id; an answer with an error names none.
func (t *talking) noteNamed(method string, body []byte) {
	if method != "UserOfCode" && method != "UserOfAddress" {
		return
	}
	var out struct {
		Results []json.RawMessage `json:"results"`
	}
	if json.Unmarshal(body, &out) != nil || len(out.Results) == 0 {
		return
	}
	var id int64
	if json.Unmarshal(out.Results[0], &id) != nil || id <= 0 {
		return
	}
	// UserOfAddress says whether the address led anywhere.
	if method == "UserOfAddress" {
		var ok bool
		if len(out.Results) < 2 || json.Unmarshal(out.Results[1], &ok) != nil || !ok {
			return
		}
	}
	t.name(id)
}

// markUpdate notes who an update handed to the addon is from.
func (t *talking) markUpdate(raw []byte) {
	var u struct {
		Message *struct {
			From *struct{ ID int64 } `json:"from"`
		} `json:"message"`
		CallbackQuery *struct {
			From struct{ ID int64 } `json:"from"`
		} `json:"callback_query"`
	}
	if json.Unmarshal(raw, &u) != nil {
		return
	}
	if u.Message != nil && u.Message.From != nil {
		t.mark(u.Message.From.ID)
	}
	if u.CallbackQuery != nil {
		t.mark(u.CallbackQuery.From.ID)
	}
}

// recorder holds an answer of the bot's RPC, for the panel to read before the addon gets it.
type recorder struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = code, true
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.body.Write(b)
}

// guardRPC checks one call of the bot's RPC before it reaches the panel's backend.
func (s *Server) guardRPC(w http.ResponseWriter, r *http.Request, c Caller) bool {
	name := r.PathValue("method")
	rl, ok := botRPC[name]
	if !ok {
		Fail(w, http.StatusForbidden, "forbidden", "the method is not open to addons")
		return false
	}
	if rl.perm != "" && !c.Can(rl.perm) {
		Fail(w, http.StatusForbidden, "forbidden", "the addon has no permission "+rl.perm)
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		Fail(w, http.StatusBadRequest, "bad_request", "the body could not be read")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(rl.tg) == 0 && len(rl.owner) == 0 && len(rl.named) == 0 && rl.tgField < 0 {
		return true
	}
	var args []json.RawMessage
	if json.Unmarshal(body, &args) != nil {
		Fail(w, http.StatusBadRequest, "bad_request", "the body is a JSON array of the arguments")
		return false
	}
	arg := func(i int) (int64, bool) {
		var v int64
		return v, i < len(args) && json.Unmarshal(args[i], &v) == nil
	}
	quiet := func() bool {
		Fail(w, http.StatusForbidden, "not_talking", "the account is not talking to the bot")
		return false
	}
	for _, i := range rl.tg {
		if tg, ok := arg(i); !ok || !s.talk.recent(tg) {
			return quiet()
		}
	}
	if rl.tgField >= 0 {
		var req struct{ TgID int64 }
		if rl.tgField >= len(args) || json.Unmarshal(args[rl.tgField], &req) != nil || !s.talk.recent(req.TgID) {
			return quiet()
		}
	}
	for _, i := range rl.owner {
		user, ok := arg(i)
		if !ok || !s.ownerTalking(r.Context(), user) {
			return quiet()
		}
	}
	for _, i := range rl.named {
		if user, ok := arg(i); !ok || !s.talk.wasNamed(user) {
			Fail(w, http.StatusForbidden, "not_named", "the subscription was not named by a code or an address sent to the bot")
			return false
		}
	}
	return true
}

// ownerTalking: the subscription's account is talking to the bot.
func (s *Server) ownerTalking(ctx context.Context, userID int64) bool {
	link, err := s.d.Store.Q.GetTgLink(ctx, userID)
	return err == nil && s.talk.recent(link.TgID)
}
