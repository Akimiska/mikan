package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgbackup"
	"mikan/internal/panel/tgbot"
)

// Backups of the database to the admin's Telegram chat (tgbackup). Everything here is the
// session's: a backup holds every secret of the panel, and its password opens it.

type BackupView struct {
	Enabled      bool       `json:"enabled" doc:"Слать базу в чат администратора раз в сутки"`
	Hour         int        `json:"hour" doc:"Час отправки (UTC)"`
	PasswordSet  bool       `json:"password_set" doc:"Пароль шифрования задан; сам пароль не возвращается"`
	AdminChatSet bool       `json:"admin_chat_set" doc:"Чат администратора подключён к боту (вкладка «Инфраструктура»)"`
	LastOK       *time.Time `json:"last_ok,omitempty" doc:"Когда ушёл последний бэкап"`
	LastTry      *time.Time `json:"last_try,omitempty"`
	LastError    string     `json:"last_error,omitempty" doc:"Код ошибки последней попытки"`
	LastSize     int64      `json:"last_size,omitempty" doc:"Размер последнего файла, байт"`
}

type backupOutput struct{ Body BackupView }

type patchBackupInput struct {
	Body struct {
		Enabled  *bool   `json:"enabled,omitempty"`
		Hour     *int    `json:"hour,omitempty" minimum:"0" maximum:"23"`
		Password *string `json:"password,omitempty" maxLength:"128" doc:"Пароль, которым шифруется файл: от 12 символов. Без него бэкап не открыть"`
	}
}

func (h *handlers) registerBackups() {
	tags := []string{"telegram"}
	huma.Register(h.api, huma.Operation{OperationID: "get-telegram-backup", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodGet, Path: "/api/v1/telegram/backup", Summary: "Бэкапы в Telegram", Tags: tags}, h.getBackup)
	huma.Register(h.api, huma.Operation{OperationID: "update-telegram-backup", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPatch, Path: "/api/v1/telegram/backup", Summary: "Настроить бэкапы в Telegram", Tags: tags}, h.updateBackup)
	huma.Register(h.api, huma.Operation{OperationID: "send-telegram-backup", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/telegram/backup/send", Summary: "Отправить бэкап сейчас", Tags: tags}, h.sendBackup)
}

func (h *handlers) backupView(ctx context.Context) (BackupView, error) {
	var v BackupView
	var err error
	if v.Enabled, err = h.d.Settings.On(ctx, tgbackup.Enabled); err != nil {
		return v, err
	}
	hour, ok, err := settings.Get[int](ctx, h.d.Settings, tgbackup.KeyHour)
	if err != nil {
		return v, err
	}
	v.Hour = tgbackup.DefaultHour
	if ok {
		v.Hour = hour
	}
	pw, err := h.d.Settings.String(ctx, tgbackup.KeyPassword)
	if err != nil {
		return v, err
	}
	v.PasswordSet = len(pw) >= tgbackup.MinPassword
	if h.d.Telegram != nil {
		_, v.AdminChatSet, _ = h.d.Telegram.InfrastructureAdminChat(ctx)
	}
	st, _, err := settings.Get[tgbackup.State](ctx, h.d.Settings, tgbackup.KeyState)
	if err != nil {
		return v, err
	}
	at := func(unix int64) *time.Time {
		if unix == 0 {
			return nil
		}
		t := time.Unix(unix, 0).UTC()
		return &t
	}
	v.LastOK, v.LastTry, v.LastError, v.LastSize = at(st.LastOK), at(st.LastTry), st.LastError, st.LastSize
	return v, nil
}

func (h *handlers) getBackup(ctx context.Context, _ *struct{}) (*backupOutput, error) {
	v, err := h.backupView(ctx)
	if err != nil {
		return nil, err
	}
	return &backupOutput{Body: v}, nil
}

func (h *handlers) updateBackup(ctx context.Context, in *patchBackupInput) (*backupOutput, error) {
	b := in.Body
	if b.Password != nil && len(*b.Password) < tgbackup.MinPassword {
		return nil, tgFieldErr("password", "backup_password_short")
	}
	if b.Enabled != nil && *b.Enabled && b.Password == nil {
		pw, err := h.d.Settings.String(ctx, tgbackup.KeyPassword)
		if err != nil {
			return nil, err
		}
		if len(pw) < tgbackup.MinPassword {
			return nil, tgFieldErr("password", "no_backup_password")
		}
	}
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		set := settings.New(q)
		if b.Password != nil {
			if err := settings.Set(ctx, set, tgbackup.KeyPassword, *b.Password); err != nil {
				return err
			}
		}
		if b.Hour != nil {
			if err := settings.Set(ctx, set, tgbackup.KeyHour, *b.Hour); err != nil {
				return err
			}
		}
		if b.Enabled != nil {
			return settings.Set(ctx, set, tgbackup.KeyEnabled, *b.Enabled)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	details := map[string]any{}
	if b.Enabled != nil {
		details["enabled"] = *b.Enabled
	}
	if b.Hour != nil {
		details["hour"] = *b.Hour
	}
	if b.Password != nil {
		details["password"] = "changed"
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "telegram.backup.update", "telegram", "", details)
	return h.getBackup(ctx, nil)
}

func (h *handlers) sendBackup(ctx context.Context, _ *struct{}) (*backupOutput, error) {
	if h.d.Backups == nil {
		return nil, huma.Error503ServiceUnavailable("bot_unavailable")
	}
	_, err := h.d.Backups.Send(ctx)
	var ae *tgbot.APIError
	switch {
	case errors.Is(err, tgbackup.ErrNoChat), errors.Is(err, tgbackup.ErrNoPassword), errors.Is(err, tgbackup.ErrTooBig), errors.Is(err, tgbackup.ErrBusy):
		return nil, huma.Error409Conflict(err.Error())
	case errors.Is(err, tgbot.ErrOff):
		return nil, huma.Error409Conflict("bot_off")
	case errors.Is(err, tgbot.ErrUnreachable), errors.As(err, &ae):
		return nil, huma.Error502BadGateway("tg_unreachable")
	case err != nil:
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "telegram.backup.send", "telegram", "", nil)
	return h.getBackup(ctx, nil)
}
