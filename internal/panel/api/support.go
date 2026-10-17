package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/audit"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/server"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/support"
	"mikan/internal/release"
)

// Support access (internal/panel/support keeps the grants). Whoever the admin lets in works
// with the panel as a session or as a bearer token "ms_...", for as long as the grant lives,
// and only through what this file lets through: supportGate decides, per operation.

const keySupport ctxKey = 101

// supportOperatorTag marks the two operations the operator itself uses (whoami, enter).
// The admin's own are tagged supportAdminTag, which no operator reaches.
const (
	supportAdminTag    = "support"
	supportOperatorTag = "support-operator"
	// supportActionsShown is how many lines of the operator's journal the grant's card holds.
	supportActionsShown = 100
)

// supportDeniedTags and supportDeniedOps are what a support operator never reaches, at any
// level: logins and the admin's own credentials (auth, api-keys, support: the grants
// themselves), the way into the admin (the secret path), the journal and the database in
// Telegram, a broadcast to every subscriber, and the bot itself: its token, its route and the
// admin's chat for notices. The admin learns of an operator's entry and of a terminal only
// through that chat, and the bot token carries the subscribers' conversations and the Stars,
// so an operator who could mute, redirect or swap them would work unseen.
var (
	supportDeniedTags = []string{"auth", "api-keys", supportAdminTag}
	supportDeniedOps  = []string{"list-audit", "reset-admin-path", "get-telegram-backup", "update-telegram-backup", "send-telegram-backup", "telegram-broadcast",
		"update-telegram", "telegram-infrastructure-connect", "telegram-infrastructure-disconnect"}
)

type supportCtx struct {
	grant db.SupportGrant
	via   string // "browser" or "api"
}

// supportGrantOf is the support grant a request works under; false: the admin's own
// session or an API key.
func supportGrantOf(ctx context.Context) (db.SupportGrant, bool) {
	s, ok := ctx.Value(keySupport).(supportCtx)
	return s.grant, ok
}

// supportTerminalAllowed tells whether the request may open a terminal on target ("panel" or
// "node:<id>"): it comes from a support grant of the terminal level that names the server.
// The admin's own session and API keys have no terminal here.
func supportTerminalAllowed(ctx context.Context, target string) bool {
	g, ok := supportGrantOf(ctx)
	return ok && support.TerminalAllowed(g, target)
}

// supportDiagnostic marks a POST that only reads: a server check or a doctor run changes
// nothing on the server, so an operator at the view level may call it. A restart is no such
// operation and stays a write.
var supportDiagnostic = map[string]any{"supportDiagnostic": true}

func supportReadOnlyPost(op *huma.Operation) bool {
	diag, _ := op.Metadata["supportDiagnostic"].(bool)
	leave, _ := op.Metadata["supportLeave"].(bool)
	return diag || leave
}

// supportGate is the decision for an operation a support operator called: 0 lets it
// through, otherwise the status and the code to refuse with. Closed operations first (the
// code says closed, not read-only), then the level: view only reads.
func supportGate(op *huma.Operation, mutating bool, g db.SupportGrant) (int, string) {
	if slices.Contains(supportDeniedOps, op.OperationID) {
		return http.StatusForbidden, "support_denied"
	}
	for _, tag := range op.Tags {
		if slices.Contains(supportDeniedTags, tag) {
			return http.StatusForbidden, "support_denied"
		}
	}
	if mutating && !support.Writes(g.Level) && !supportReadOnlyPost(op) {
		return http.StatusForbidden, "support_read_only"
	}
	return 0, ""
}

// hidesLinks: the credentials of the subscribers, their subscription links (and the old
// ones of an imported panel), stay out of the answers of a "read" key, and of an operator
// the admin did not let see the subscribers.
func hidesLinks(ctx context.Context) bool {
	if k, ok := apiKeyOf(ctx); ok && k.Scope != "full" {
		return true
	}
	g, ok := supportGrantOf(ctx)
	return ok && !g.ShowPeople
}

// hidesPeople: a support operator the admin did not let see the subscribers. What would
// answer a question about a person by its count or its order (a search) is closed to it.
func hidesPeople(ctx context.Context) bool {
	g, ok := supportGrantOf(ctx)
	return ok && !g.ShowPeople
}

// supportActor is "support:<grant id>" for a request under a grant.
func supportActor(ctx context.Context) (string, bool) {
	g, ok := supportGrantOf(ctx)
	if !ok {
		return "", false
	}
	return audit.ActorSupport(g.ID), true
}

// supportBearer lets in a request that came with "Bearer ms_...". The caller has counted the
// address against its allowance and failed it when this answers 401.
func (h *handlers) supportBearer(ctx huma.Context, next func(huma.Context), mutating bool, token, ipKey string) {
	now := h.d.Now()
	var g db.SupportGrant
	err := support.ErrInvalid
	if h.d.Support != nil {
		g, err = h.d.Support.Authenticate(ctx.Context(), token)
	}
	if errors.Is(err, support.ErrInvalid) {
		h.d.IPLimit.Fail(ipKey, now)
		_ = huma.WriteErr(h.api, ctx, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		h.d.Log.Error("support grant lookup", "err", err)
		_ = huma.WriteErr(h.api, ctx, http.StatusInternalServerError, "internal error")
		return
	}
	h.supportServe(ctx, next, mutating, g, db.Session{AdminID: g.AdminID}, "api")
}

// supportServe runs a request of an operator whose grant is live: the gate, the note that the
// operator is there, and the handler with the grant in its context. The session the handler
// sees is the admin's who opened the grant, so that what it reads and writes is the admin's;
// what it is allowed to do was decided by the gate above.
func (h *handlers) supportServe(ctx huma.Context, next func(huma.Context), mutating bool, g db.SupportGrant, sess db.Session, via string) {
	if status, code := supportGate(ctx.Operation(), mutating, g); status != 0 {
		_ = huma.WriteErr(h.api, ctx, status, code)
		return
	}
	h.d.Support.Used(ctx.Context(), g, clientOf(ctx.Context()).IP, via)
	if !g.ShowPeople {
		if m, ok := ctx.Context().Value(keyMask).(*maskState); ok {
			m.on.Store(true)
		}
	}
	ctx = huma.WithValue(ctx, keySession, sess)
	next(huma.WithValue(ctx, keySupport, supportCtx{grant: g, via: via}))
}

// supportSession serves a request of a browser session that belongs to a grant, which must
// still be live: the session ends with it.
func (h *handlers) supportSession(ctx huma.Context, next func(huma.Context), mutating bool, sess db.Session) {
	var g db.SupportGrant
	err := support.ErrInvalid
	if h.d.Support != nil {
		g, err = h.d.Support.Get(ctx.Context(), sess.SupportGrantID.Int64)
	}
	if errors.Is(err, support.ErrInvalid) {
		if derr := h.d.Store.Q.DeleteSession(ctx.Context(), sess.IDHash); derr != nil {
			h.d.Log.Warn("support session cleanup", "err", derr)
		}
		_ = huma.WriteErr(h.api, ctx, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		h.d.Log.Error("support grant lookup", "err", err)
		_ = huma.WriteErr(h.api, ctx, http.StatusInternalServerError, "internal error")
		return
	}
	// The session is the grant's, whatever admin id it carries.
	sess.AdminID = g.AdminID
	h.supportServe(ctx, next, mutating, g, sess, "browser")
}

// supportFromRequest resolves a plain HTTP request that is no huma operation (a WebSocket's
// upgrade) to the live support grant of its cookie session or its "Bearer ms_..." token, and
// notes the use. false: no live grant, whatever the reason; the caller answers 401. It looks
// at neither the level nor the target (supportTerminalAllowed does, on a context that carries
// the grant: withSupportGrant) nor the request's origin (the caller's own check).
func (h *handlers) supportFromRequest(r *http.Request) (db.SupportGrant, bool, error) {
	if h.d.Support == nil {
		return db.SupportGrant{}, false, nil
	}
	ctx, ip := r.Context(), server.ClientIP(r.Header, r.RemoteAddr, h.d.TrustProxy)
	if hdr := r.Header.Get("Authorization"); hdr != "" {
		token, ok := strings.CutPrefix(hdr, "Bearer ")
		token = strings.TrimSpace(token)
		if !ok || !strings.HasPrefix(token, support.TokenPrefix) {
			return db.SupportGrant{}, false, nil
		}
		ipKey, now := "key:"+auth.ClientKey(ip), h.d.Now()
		if allowed, _ := h.d.IPLimit.Allowed(ipKey, now); !allowed {
			return db.SupportGrant{}, false, nil
		}
		g, err := h.d.Support.Authenticate(ctx, token)
		if errors.Is(err, support.ErrInvalid) {
			h.d.IPLimit.Fail(ipKey, now)
			return db.SupportGrant{}, false, nil
		}
		if err != nil {
			return db.SupportGrant{}, false, err
		}
		h.d.Support.Used(ctx, g, ip, "api")
		return g, true, nil
	}
	ck, err := r.Cookie(auth.CookieName)
	if err != nil {
		return db.SupportGrant{}, false, nil
	}
	sess, err := h.d.Sessions.Lookup(ctx, ck.Value)
	if errors.Is(err, auth.ErrNoSession) || err == nil && !sess.SupportGrantID.Valid {
		return db.SupportGrant{}, false, nil
	}
	if err != nil {
		return db.SupportGrant{}, false, err
	}
	g, err := h.d.Support.Get(ctx, sess.SupportGrantID.Int64)
	if errors.Is(err, support.ErrInvalid) {
		return db.SupportGrant{}, false, nil
	}
	if err != nil {
		return db.SupportGrant{}, false, err
	}
	h.d.Support.Used(ctx, g, ip, "browser")
	return g, true, nil
}

// withSupportGrant is a context that carries a grant, as a request through the middleware
// does: what supportTerminalAllowed and supportActor read.
func withSupportGrant(ctx context.Context, g db.SupportGrant, via string) context.Context {
	return context.WithValue(ctx, keySupport, supportCtx{grant: g, via: via})
}

// What the admin sees of a grant.
type SupportGrantView struct {
	ID              int64      `json:"id"`
	Level           string     `json:"level" enum:"view,change,terminal" doc:"view: только чтение; change: ещё и изменения; terminal: ещё и терминал на серверах из terminal_targets"`
	ShowPeople      bool       `json:"show_people" doc:"Оператор видит имена, Telegram и ссылки подписок; иначе они скрыты"`
	TerminalTargets []string   `json:"terminal_targets" doc:"Где разрешён терминал: panel или node:<id>; пусто вне уровня terminal"`
	Note            string     `json:"note"`
	Status          string     `json:"status" enum:"active,expired,revoked"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	EnteredAt       *time.Time `json:"entered_at" doc:"Когда оператор вошёл по ссылке в браузере; null: ещё нет"`
	LastUsedAt      *time.Time `json:"last_used_at" doc:"Последнее обращение оператора (браузер или ключ); null: ещё не было"`
	LastIP          string     `json:"last_ip" doc:"Адрес, с которого оператор был в последний раз"`
	RevokedAt       *time.Time `json:"revoked_at"`
}

func (h *handlers) viewSupportGrant(g db.SupportGrant) SupportGrantView {
	status := "active"
	switch {
	case g.RevokedAt.Valid:
		status = "revoked"
	case !support.Live(g, h.d.Now()):
		status = "expired"
	}
	targets := g.TerminalTargets
	if targets == nil {
		targets = []string{}
	}
	return SupportGrantView{ID: g.ID, Level: g.Level, ShowPeople: g.ShowPeople, TerminalTargets: targets, Note: g.Note, Status: status,
		CreatedAt: time.Unix(g.CreatedAt, 0).UTC(), ExpiresAt: support.Expires(g), EnteredAt: store.TimePtr(g.EnteredAt),
		LastUsedAt: store.TimePtr(g.LastUsedAt), LastIP: g.LastIp, RevokedAt: store.TimePtr(g.RevokedAt)}
}

type createSupportGrantInput struct {
	Body struct {
		Code            string   `json:"code" minLength:"8" maxLength:"16" doc:"Одноразовый код из обращения в боте поддержки: без него доступ не открывается"`
		Hours           int      `json:"hours" enum:"2,6,24" doc:"Срок доступа в часах"`
		Level           string   `json:"level" enum:"view,change,terminal"`
		ShowPeople      bool     `json:"show_people,omitempty" doc:"Показывать оператору имена, Telegram и ссылки подписчиков"`
		TerminalTargets []string `json:"terminal_targets,omitempty" maxItems:"64" doc:"Уровень terminal: где разрешён терминал, panel и/или node:<id>. Полный доступ к этим серверам"`
		Note            string   `json:"note,omitempty" maxLength:"200" doc:"Пометка для себя, например номер обращения"`
		Password        string   `json:"password,omitempty" maxLength:"256" doc:"Пароль админа: нужен для уровня terminal"`
		TOTP            string   `json:"totp,omitempty" maxLength:"16" doc:"Код из приложения, если включена 2FA: нужен для уровня terminal"`
	}
}

type createSupportGrantOutput struct {
	Body struct {
		Grant  SupportGrantView `json:"grant"`
		Ticket int64            `json:"ticket" doc:"Обращение в боте поддержки, которому передан доступ"`
	}
}

type supportGrantIDInput struct {
	ID int64 `path:"id" minimum:"1"`
}

type supportGrantsOutput struct {
	Body struct {
		Off     string            `json:"off" enum:",env,setting" doc:"Выключен ли доступ для поддержки: пусто — включён, env — MIKAN_SUPPORT_ACCESS=off в .env сервера, setting — настройкой support_access"`
		Grant   *SupportGrantView `json:"grant" doc:"Последний выданный доступ, активный или нет; null: ещё не выдавался"`
		Actions []AuditEntry      `json:"actions" doc:"Что делал оператор и что было с доступом, от новых к старым"`
	}
}

type supportEnterInput struct {
	Body struct {
		Token string `json:"token" minLength:"43" maxLength:"43" doc:"Токен из ссылки доступа (после #)"`
	}
}

type supportEnterOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      SupportWhoami
}

type SupportNode struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type SupportWhoami struct {
	Level           string        `json:"level" enum:"view,change,terminal"`
	ExpiresAt       time.Time     `json:"expires_at"`
	ShowPeople      bool          `json:"show_people"`
	PanelVersion    string        `json:"panel_version"`
	ReleaseKey      string        `json:"release_key" doc:"Отпечаток ключа подписи релизов, которому верит эта сборка: у официального mikan он один, у форка свой"`
	Nodes           []SupportNode `json:"nodes"`
	TerminalTargets []string      `json:"terminal_targets"`
	CSRFToken       string        `json:"csrf_token,omitempty" doc:"Для изменяющих запросов сессии оператора; у ключа ms_… нет"`
}

type supportWhoamiOutput struct{ Body SupportWhoami }

func (h *handlers) registerSupport() {
	admin := []string{supportAdminTag}
	huma.Register(h.api, huma.Operation{OperationID: "create-support-grant", Method: http.MethodPost, Path: "/api/v1/support/grants", Summary: "Открыть доступ для поддержки",
		Description: "Только для официальной поддержки: панель проверяет код из обращения у бота поддержки, открывает доступ и сама передаёт ссылку боту, админу она не показывается. " +
			"Одновременно активен один доступ: новый отзывает прежний. Уровень terminal требует пароль и код 2FA.",
		Tags: admin, Metadata: sessionOnly, Extensions: sessionOnlyExt, DefaultStatus: http.StatusCreated}, h.createSupportGrant)
	huma.Register(h.api, huma.Operation{OperationID: "list-support-grants", Method: http.MethodGet, Path: "/api/v1/support/grants", Summary: "Доступ для поддержки и действия оператора",
		Tags: admin, Metadata: sessionOnly, Extensions: sessionOnlyExt}, h.listSupportGrants)
	huma.Register(h.api, huma.Operation{OperationID: "revoke-support-grant", Method: http.MethodDelete, Path: "/api/v1/support/grants/{id}", Summary: "Отозвать доступ для поддержки",
		Description: "Оператор теряет доступ сразу: его сессии удаляются, ключ ms_… перестаёт работать.",
		Tags:        admin, Metadata: sessionOnly, Extensions: sessionOnlyExt, DefaultStatus: http.StatusNoContent}, h.revokeSupportGrant)
	huma.Register(h.api, huma.Operation{OperationID: "support-enter", Method: http.MethodPost, Path: "/api/v1/support/enter", Summary: "Вход оператора по ссылке доступа",
		Description: "Одна ссылка пускает в браузер один раз; повторный вход требует новой ссылки. Ключ ms_… в заголовке Authorization работает, пока доступ жив.",
		Tags:        []string{supportOperatorTag}, Metadata: map[string]any{"public": true}, Security: []map[string][]string{{}}}, h.supportEnter)
	huma.Register(h.api, huma.Operation{OperationID: "support-leave", Method: http.MethodPost, Path: "/api/v1/support/leave", Summary: "Закончить работу и выйти",
		Description: "Оператор сам закрывает свой доступ: как отзыв админом, сессии и терминалы закрываются, ключ ms_… перестаёт работать. На любом уровне. Другим отвечает 403 support_only.",
		Tags:        []string{supportOperatorTag}, Metadata: map[string]any{"supportLeave": true}, DefaultStatus: http.StatusNoContent}, h.supportLeave)
	huma.Register(h.api, huma.Operation{OperationID: "support-whoami", Method: http.MethodGet, Path: "/api/v1/support/whoami", Summary: "Чей доступ и до какого времени",
		Description: "Для оператора: сессия или ключ ms_…. Другим отвечает 403 support_only.",
		Tags:        []string{supportOperatorTag}}, h.supportWhoami)
}

func (h *handlers) supportLeave(ctx context.Context, _ *struct{}) (*struct{}, error) {
	g, ok := supportGrantOf(ctx)
	if !ok {
		return nil, huma.Error403Forbidden("support_only")
	}
	err := h.d.Support.Leave(ctx, g.ID, clientOf(ctx).IP)
	if err != nil && !errors.Is(err, support.ErrNotLive) {
		return nil, err
	}
	return nil, nil
}

// supportLink is the address the admin gives the operator: the panel's own, the secret path
// and the page that takes the token from the fragment, which a server never sees in a log.
func (h *handlers) supportLink(ctx context.Context, token string) (string, error) {
	paths, err := h.d.Settings.Paths(ctx)
	if err != nil {
		return "", err
	}
	ep, err := h.d.Settings.Endpoint(ctx)
	if err != nil {
		return "", err
	}
	return ep.URL() + "/" + paths.Admin + "/support#" + token, nil
}

func (h *handlers) createSupportGrant(ctx context.Context, in *createSupportGrantInput) (*createSupportGrantOutput, error) {
	// The operation is session only and closed to operators; the same refusal here, so that a
	// lost mark does not let a key or an operator mint access.
	if err := requireSession(ctx, "level"); err != nil {
		return nil, err
	}
	b := &in.Body
	admin, err := h.d.Store.Q.GetAdmin(ctx, sessionOf(ctx).AdminID)
	if err != nil {
		return nil, err
	}
	// A root shell on the servers is what a stolen session must not hand out alone.
	if b.Level == support.LevelTerminal {
		if err := h.reauth(ctx, admin, b.Password, strings.TrimSpace(b.TOTP)); err != nil {
			return nil, err
		}
	}
	if off, err := h.d.Support.Off(ctx); err != nil {
		return nil, err
	} else if off != "" {
		return nil, huma.Error403Forbidden("support_disabled")
	}
	code, ok := support.NormCode(b.Code)
	if !ok {
		return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.code", Message: "support_code"})
	}
	bot := h.d.Support.Bot()
	if bot == nil {
		return nil, huma.Error503ServiceUnavailable("support_bot_down")
	}
	// The bot says yes before anything is opened: access goes to a paid ticket only.
	ticket, err := bot.Claim(ctx, code)
	if err != nil {
		return nil, supportBotError(err)
	}
	note := b.Note
	if strings.TrimSpace(note) == "" {
		note = "#" + strconv.FormatInt(ticket, 10)
	}
	ip := clientOf(ctx).IP
	created, err := h.d.Support.Create(ctx, support.CreateInput{AdminID: admin.ID, Hours: b.Hours, Level: b.Level, ShowPeople: b.ShowPeople,
		Targets: b.TerminalTargets, Note: note, IP: ip})
	if errors.Is(err, support.ErrDisabled) {
		return nil, huma.Error403Forbidden("support_disabled")
	}
	var fe *support.FieldError
	if errors.As(err, &fe) {
		return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body." + fe.Field, Message: fe.Code})
	}
	if err != nil {
		return nil, err
	}
	link, err := h.supportLink(ctx, created.Token)
	if err == nil {
		ticket, err = bot.Attach(ctx, code, link)
	}
	if err != nil {
		// The link did not reach the bot: nobody holds it, the grant goes.
		if werr := h.d.Support.Withdraw(context.WithoutCancel(ctx), created.Grant.ID, admin.ID, ip); werr != nil {
			h.d.Log.Error("support: withdraw an undelivered grant", "grant", created.Grant.ID, "err", werr)
		}
		return nil, supportBotError(err)
	}
	out := &createSupportGrantOutput{}
	out.Body.Grant, out.Body.Ticket = h.viewSupportGrant(created.Grant), ticket
	return out, nil
}

// supportBotError is what the admin is told when the support bot said no or did not answer.
func supportBotError(err error) error {
	var le *support.LinkError
	switch {
	case errors.Is(err, support.ErrCode):
		return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.code", Message: "support_code_invalid"})
	case errors.As(err, &le):
		return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.code", Message: "support_link_refused", Value: le.Reason})
	case errors.Is(err, support.ErrBotDown):
		return huma.Error502BadGateway("support_bot_down")
	}
	return err
}

func (h *handlers) listSupportGrants(ctx context.Context, _ *struct{}) (*supportGrantsOutput, error) {
	// A grant whose time ran out is noted before it is shown.
	if err := h.d.Support.Sweep(ctx); err != nil {
		return nil, err
	}
	out := &supportGrantsOutput{}
	out.Body.Actions = []AuditEntry{}
	off, err := h.d.Support.Off(ctx)
	if err != nil {
		return nil, err
	}
	out.Body.Off = off
	g, err := h.d.Store.Q.LatestSupportGrant(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	v := h.viewSupportGrant(g)
	out.Body.Grant = &v
	rows, err := h.d.Store.Q.SupportAudit(ctx, db.SupportAuditParams{GrantID: strconv.FormatInt(g.ID, 10), Lim: supportActionsShown})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Body.Actions = append(out.Body.Actions, auditEntry(r))
	}
	return out, nil
}

func (h *handlers) revokeSupportGrant(ctx context.Context, in *supportGrantIDInput) (*struct{}, error) {
	if err := requireSession(ctx, "id"); err != nil {
		return nil, err
	}
	_, err := h.d.Support.Revoke(ctx, in.ID, sessionOf(ctx).AdminID, clientOf(ctx).IP)
	if errors.Is(err, support.ErrNotLive) {
		return nil, huma.Error404NotFound("not_found")
	}
	return nil, err
}

// supportEnter opens the operator's browser session from the link's token. A token that does
// not open a live grant counts against the address's allowance, like a wrong API key.
func (h *handlers) supportEnter(ctx context.Context, in *supportEnterInput) (*supportEnterOutput, error) {
	c := clientOf(ctx)
	ipKey, now := "support:"+auth.ClientKey(c.IP), h.d.Now()
	if ok, wait := h.d.IPLimit.Allowed(ipKey, now); !ok {
		return nil, tooManyAttempts(wait)
	}
	var g db.SupportGrant
	err := support.ErrInvalid
	if h.d.Support != nil {
		g, err = h.d.Support.Authenticate(ctx, in.Body.Token)
	}
	if errors.Is(err, support.ErrInvalid) {
		h.d.IPLimit.Fail(ipKey, now)
		return nil, huma.Error401Unauthorized("invalid_support_token")
	}
	if err != nil {
		return nil, err
	}
	switch err := h.d.Support.Enter(ctx, g, c.IP); {
	case errors.Is(err, support.ErrUsed):
		return nil, huma.Error409Conflict("support_link_used")
	case errors.Is(err, support.ErrInvalid):
		return nil, huma.Error401Unauthorized("invalid_support_token")
	case err != nil:
		return nil, err
	}
	token, sess, err := h.d.Sessions.CreateSupport(ctx, g.AdminID, g.ID, g.ExpiresAt, c.IP, c.UserAgent)
	if err != nil {
		return nil, err
	}
	// The cookie ends with the grant, by the panel's clock.
	out := &supportEnterOutput{SetCookie: sessionCookie(token, support.Expires(g).Sub(now))}
	out.Body, err = h.supportWhoamiBody(ctx, g, sess.CsrfToken)
	return out, err
}

func (h *handlers) supportWhoamiBody(ctx context.Context, g db.SupportGrant, csrf string) (SupportWhoami, error) {
	nodes, err := h.d.Store.Q.ListNodes(ctx)
	if err != nil {
		return SupportWhoami{}, err
	}
	targets := g.TerminalTargets
	if targets == nil {
		targets = []string{}
	}
	w := SupportWhoami{Level: g.Level, ExpiresAt: support.Expires(g), ShowPeople: g.ShowPeople, PanelVersion: h.d.Version, ReleaseKey: release.KeyFingerprint(),
		Nodes: make([]SupportNode, 0, len(nodes)), TerminalTargets: targets, CSRFToken: csrf}
	for _, n := range nodes {
		w.Nodes = append(w.Nodes, SupportNode{ID: n.ID, Name: n.Name})
	}
	return w, nil
}

func (h *handlers) supportWhoami(ctx context.Context, _ *struct{}) (*supportWhoamiOutput, error) {
	g, ok := supportGrantOf(ctx)
	if !ok {
		return nil, huma.Error403Forbidden("support_only")
	}
	csrf := ""
	// The operator's browser needs the token for its changing requests; a bearer call does not.
	if s := sessionOf(ctx); s.SupportGrantID.Valid {
		csrf = s.CsrfToken
	}
	body, err := h.supportWhoamiBody(ctx, g, csrf)
	if err != nil {
		return nil, err
	}
	return &supportWhoamiOutput{Body: body}, nil
}
