package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/auth"
	"mikan/internal/panel/store/db"
)

type AdminView struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	TOTPEnabled bool       `json:"totp_enabled"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type MeBody struct {
	Admin     AdminView `json:"admin"`
	CSRFToken string    `json:"csrf_token"`
}

func viewAdmin(a db.Admin) AdminView {
	v := AdminView{ID: a.ID, Username: a.Username, TOTPEnabled: a.TotpSecret.Valid}
	if a.LastLoginAt.Valid {
		t := time.Unix(a.LastLoginAt.Int64, 0).UTC()
		v.LastLoginAt = &t
	}
	return v
}

type loginInput struct {
	Body struct {
		Username string `json:"username" minLength:"1" maxLength:"64"`
		Password string `json:"password" minLength:"1" maxLength:"256"`
		TOTP     string `json:"totp,omitempty" maxLength:"16" doc:"Код из приложения или резервный код"`
	}
}

type meOutput struct {
	Body MeBody
}

type loginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      MeBody
}

type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type passwordInput struct {
	Body struct {
		Current string `json:"current" minLength:"1" maxLength:"256"`
		New     string `json:"new" minLength:"12" maxLength:"256" doc:"Не короче 12 символов"`
	}
}

type totpSetupOutput struct {
	Body struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
	}
}

type totpCodeInput struct {
	Body struct {
		Code string `json:"code" minLength:"6" maxLength:"16"`
	}
}

type totpDisableInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"256"`
		Code     string `json:"code" minLength:"6" maxLength:"16"`
	}
}

type recoveryOutput struct {
	Body struct {
		RecoveryCodes []string `json:"recovery_codes" doc:"Показываются один раз"`
	}
}

type SessionView struct {
	ID         string    `json:"id"`
	Current    bool      `json:"current"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type sessionsOutput struct {
	Body []SessionView
}

type sessionIDInput struct {
	ID string `path:"id" minLength:"64" maxLength:"64"`
}

type pendingTOTP struct {
	secret  string
	expires time.Time
}

func (h *handlers) registerAuth() {
	public := map[string]any{"public": true}
	huma.Register(h.api, huma.Operation{OperationID: "login", Method: http.MethodPost, Path: "/api/v1/auth/login", Summary: "Вход", Tags: []string{"auth"}, Metadata: public}, h.login)
	huma.Register(h.api, huma.Operation{OperationID: "logout", Method: http.MethodPost, Path: "/api/v1/auth/logout", Summary: "Выход", Tags: []string{"auth"}, DefaultStatus: http.StatusNoContent}, h.logout)
	huma.Register(h.api, huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/api/v1/auth/me", Summary: "Текущий админ", Tags: []string{"auth"}}, h.me)
	huma.Register(h.api, huma.Operation{OperationID: "change-password", Method: http.MethodPost, Path: "/api/v1/auth/password", Summary: "Сменить пароль", Tags: []string{"auth"}, DefaultStatus: http.StatusNoContent}, h.changePassword)
	huma.Register(h.api, huma.Operation{OperationID: "totp-setup", Method: http.MethodPost, Path: "/api/v1/auth/totp/setup", Summary: "Начать настройку 2FA", Tags: []string{"auth"}}, h.totpSetup)
	huma.Register(h.api, huma.Operation{OperationID: "totp-enable", Method: http.MethodPost, Path: "/api/v1/auth/totp/enable", Summary: "Включить 2FA", Tags: []string{"auth"}}, h.totpEnable)
	huma.Register(h.api, huma.Operation{OperationID: "totp-disable", Method: http.MethodPost, Path: "/api/v1/auth/totp/disable", Summary: "Выключить 2FA", Tags: []string{"auth"}, DefaultStatus: http.StatusNoContent}, h.totpDisable)
	huma.Register(h.api, huma.Operation{OperationID: "list-sessions", Method: http.MethodGet, Path: "/api/v1/auth/sessions", Summary: "Активные сессии", Tags: []string{"auth"}}, h.listSessions)
	huma.Register(h.api, huma.Operation{OperationID: "revoke-session", Method: http.MethodDelete, Path: "/api/v1/auth/sessions/{id}", Summary: "Завершить сессию", Tags: []string{"auth"}, DefaultStatus: http.StatusNoContent}, h.revokeSession)
}

func sessionCookie(value string, maxAge time.Duration) http.Cookie {
	c := http.Cookie{Name: auth.CookieName, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
	if maxAge > 0 {
		c.MaxAge = int(maxAge / time.Second)
	} else {
		c.MaxAge = -1
	}
	return c
}

func tooManyAttempts(wait time.Duration) error {
	secs := int(wait.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return huma.ErrorWithHeaders(huma.Error429TooManyRequests("rate_limited"), http.Header{"Retry-After": {strconv.Itoa(secs)}})
}

func (h *handlers) login(ctx context.Context, in *loginInput) (*loginOutput, error) {
	c := clientOf(ctx)
	now := h.d.Now()
	username := strings.ToLower(strings.TrimSpace(in.Body.Username))
	ipKey, userKey := "ip:"+c.IP, "user:"+username
	if ok, wait := h.d.IPLimit.Allowed(ipKey, now); !ok {
		return nil, tooManyAttempts(wait)
	}
	if ok, wait := h.d.UserLimit.Allowed(userKey, now); !ok {
		return nil, tooManyAttempts(wait)
	}

	admin, err := h.d.Store.Q.GetAdminByUsername(ctx, username)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	hash := h.dummyHash
	if found {
		hash = admin.PasswordHash
	}
	// The dummy hash keeps timing identical for unknown usernames.
	ok, err := auth.VerifyPassword(in.Body.Password, hash)
	if err != nil && found {
		return nil, err
	}
	if !found || !ok {
		h.loginFailed(ctx, ipKey, userKey, username, "bad_password")
		return nil, huma.Error401Unauthorized("invalid_credentials")
	}

	if admin.TotpSecret.Valid {
		code := strings.TrimSpace(in.Body.TOTP)
		if code == "" {
			return nil, huma.Error401Unauthorized("totp_required")
		}
		if !h.d.TOTP.Validate(admin.ID, admin.TotpSecret.String, code, now) {
			rest, used := auth.UseRecoveryCode(admin.RecoveryCodes.String, code)
			if !used {
				h.loginFailed(ctx, ipKey, userKey, username, "bad_totp")
				return nil, huma.Error401Unauthorized("invalid_totp")
			}
			if err := h.d.Store.Q.SetAdminRecoveryCodes(ctx, db.SetAdminRecoveryCodesParams{RecoveryCodes: sql.NullString{String: rest, Valid: true}, ID: admin.ID}); err != nil {
				return nil, err
			}
			h.audit(ctx, admin.ID, "auth.recovery_code_used", "admin", admin.Username, nil)
		}
	}

	h.d.IPLimit.Reset(ipKey)
	h.d.UserLimit.Reset(userKey)
	token, sess, err := h.d.Sessions.Create(ctx, admin.ID, c.IP, c.UserAgent)
	if err != nil {
		return nil, err
	}
	if err := h.d.Store.Q.SetAdminLastLogin(ctx, db.SetAdminLastLoginParams{LastLoginAt: sql.NullInt64{Int64: now.Unix(), Valid: true}, ID: admin.ID}); err != nil {
		return nil, err
	}
	h.audit(ctx, admin.ID, "auth.login", "admin", admin.Username, nil)
	return &loginOutput{SetCookie: sessionCookie(token, auth.MaxTTL), Body: MeBody{Admin: viewAdmin(admin), CSRFToken: sess.CsrfToken}}, nil
}

func (h *handlers) loginFailed(ctx context.Context, ipKey, userKey, username, reason string) {
	now := h.d.Now()
	blockedIP := h.d.IPLimit.Fail(ipKey, now)
	blockedUser := h.d.UserLimit.Fail(userKey, now)
	h.audit(ctx, 0, "auth.login_failed", "admin", username, map[string]any{"reason": reason, "blocked": blockedIP || blockedUser})
}

func (h *handlers) logout(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
	sess := sessionOf(ctx)
	if err := h.d.Store.Q.DeleteSession(ctx, sess.IDHash); err != nil {
		return nil, err
	}
	h.audit(ctx, sess.AdminID, "auth.logout", "", "", nil)
	return &logoutOutput{SetCookie: sessionCookie("", 0)}, nil
}

func (h *handlers) me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	sess := sessionOf(ctx)
	admin, err := h.d.Store.Q.GetAdmin(ctx, sess.AdminID)
	if err != nil {
		return nil, err
	}
	return &meOutput{Body: MeBody{Admin: viewAdmin(admin), CSRFToken: sess.CsrfToken}}, nil
}

func (h *handlers) changePassword(ctx context.Context, in *passwordInput) (*struct{}, error) {
	sess := sessionOf(ctx)
	admin, err := h.d.Store.Q.GetAdmin(ctx, sess.AdminID)
	if err != nil {
		return nil, err
	}
	ok, err := auth.VerifyPassword(in.Body.Current, admin.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, huma.Error422UnprocessableEntity("wrong_password", &huma.ErrorDetail{Location: "body.current", Message: "Неверный текущий пароль"})
	}
	hash, err := auth.HashPassword(in.Body.New)
	if err != nil {
		return nil, err
	}
	if err := h.d.Store.Q.SetAdminPassword(ctx, db.SetAdminPasswordParams{PasswordHash: hash, ID: admin.ID}); err != nil {
		return nil, err
	}
	if err := h.d.Store.Q.DeleteAdminSessionsExcept(ctx, db.DeleteAdminSessionsExceptParams{AdminID: admin.ID, IDHash: sess.IDHash}); err != nil {
		return nil, err
	}
	h.audit(ctx, admin.ID, "auth.password_changed", "admin", admin.Username, nil)
	return nil, nil
}

func (h *handlers) totpSetup(ctx context.Context, _ *struct{}) (*totpSetupOutput, error) {
	sess := sessionOf(ctx)
	admin, err := h.d.Store.Q.GetAdmin(ctx, sess.AdminID)
	if err != nil {
		return nil, err
	}
	if admin.TotpSecret.Valid {
		return nil, huma.Error409Conflict("totp_already_enabled")
	}
	key, err := auth.NewTOTPKey(admin.Username)
	if err != nil {
		return nil, err
	}
	h.pendingMu.Lock()
	h.pending[admin.ID] = pendingTOTP{secret: key.Secret(), expires: h.d.Now().Add(10 * time.Minute)}
	h.pendingMu.Unlock()
	out := &totpSetupOutput{}
	out.Body.Secret = key.Secret()
	out.Body.URI = key.URL()
	return out, nil
}

func (h *handlers) totpEnable(ctx context.Context, in *totpCodeInput) (*recoveryOutput, error) {
	sess := sessionOf(ctx)
	now := h.d.Now()
	h.pendingMu.Lock()
	p, ok := h.pending[sess.AdminID]
	h.pendingMu.Unlock()
	if !ok || now.After(p.expires) {
		return nil, huma.Error409Conflict("totp_setup_expired")
	}
	if !h.d.TOTP.Validate(sess.AdminID, p.secret, in.Body.Code, now) {
		return nil, huma.Error422UnprocessableEntity("invalid_totp", &huma.ErrorDetail{Location: "body.code", Message: "Код не подошёл — проверьте время на телефоне"})
	}
	plain, stored := auth.NewRecoveryCodes(10)
	if err := h.d.Store.Q.SetAdminTOTP(ctx, db.SetAdminTOTPParams{
		TotpSecret:    sql.NullString{String: p.secret, Valid: true},
		RecoveryCodes: sql.NullString{String: stored, Valid: true},
		ID:            sess.AdminID,
	}); err != nil {
		return nil, err
	}
	h.pendingMu.Lock()
	delete(h.pending, sess.AdminID)
	h.pendingMu.Unlock()
	h.audit(ctx, sess.AdminID, "auth.totp_enabled", "", "", nil)
	out := &recoveryOutput{}
	out.Body.RecoveryCodes = plain
	return out, nil
}

func (h *handlers) totpDisable(ctx context.Context, in *totpDisableInput) (*struct{}, error) {
	sess := sessionOf(ctx)
	admin, err := h.d.Store.Q.GetAdmin(ctx, sess.AdminID)
	if err != nil {
		return nil, err
	}
	if !admin.TotpSecret.Valid {
		return nil, nil
	}
	ok, err := auth.VerifyPassword(in.Body.Password, admin.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok || !h.d.TOTP.Validate(admin.ID, admin.TotpSecret.String, in.Body.Code, h.d.Now()) {
		return nil, huma.Error422UnprocessableEntity("invalid_credentials", &huma.ErrorDetail{Location: "body", Message: "Неверный пароль или код"})
	}
	if err := h.d.Store.Q.SetAdminTOTP(ctx, db.SetAdminTOTPParams{ID: admin.ID}); err != nil {
		return nil, err
	}
	h.audit(ctx, admin.ID, "auth.totp_disabled", "", "", nil)
	return nil, nil
}

func (h *handlers) listSessions(ctx context.Context, _ *struct{}) (*sessionsOutput, error) {
	cur := sessionOf(ctx)
	rows, err := h.d.Store.Q.ListAdminSessions(ctx, cur.AdminID)
	if err != nil {
		return nil, err
	}
	out := &sessionsOutput{Body: make([]SessionView, 0, len(rows))}
	for _, s := range rows {
		out.Body = append(out.Body, SessionView{
			ID: s.IDHash, Current: s.IDHash == cur.IDHash, IP: s.Ip, UserAgent: s.UserAgent,
			CreatedAt: time.Unix(s.CreatedAt, 0).UTC(), LastSeenAt: time.Unix(s.LastSeenAt, 0).UTC(),
		})
	}
	return out, nil
}

func (h *handlers) revokeSession(ctx context.Context, in *sessionIDInput) (*struct{}, error) {
	cur := sessionOf(ctx)
	target, err := h.d.Store.Q.GetSession(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && target.AdminID != cur.AdminID) {
		return nil, huma.Error404NotFound("session_not_found")
	}
	if err != nil {
		return nil, err
	}
	if err := h.d.Store.Q.DeleteSession(ctx, target.IDHash); err != nil {
		return nil, err
	}
	h.audit(ctx, cur.AdminID, "auth.session_revoked", "session", target.IDHash[:12], nil)
	return nil, nil
}
