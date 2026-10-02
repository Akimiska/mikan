// Package tgbackup sends the panel's database to the admin's Telegram chat once a day,
// encrypted with a password the admin chose.
//
// Only the database goes: users, plans, settings, the bot's token. The server's .env,
// certificates and node keys stay on the server; the panel cannot read them, by design,
// and a backup sent from the panel must not reach further than the panel does.
//
// The file is a tar.gz with data/panel/backup.db inside, encrypted with age (scrypt):
//
//	age -d -o mikan.tar.gz mikan-….tar.gz.age
//	mikan restore mikan.tar.gz
package tgbackup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/age"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/tgbot"
)

const (
	KeyEnabled  = "tg_backup"          // on/off, off by default
	KeyHour     = "tg_backup_hour"     // UTC hour of the daily backup, DefaultHour when unset
	KeyPassword = "tg_backup_password" // the age passphrase; never shown back
	KeyState    = "tg_backup_state"    // State
	DefaultHour = 3
	MinPassword = 12
)

// Enabled is the switch.
var Enabled = settings.Switch{Key: KeyEnabled}

// State is what the last backups did.
type State struct {
	LastOK      int64  `json:"last_ok,omitempty"`       // unix time of the last backup sent
	LastTry     int64  `json:"last_try,omitempty"`      // unix time of the last attempt
	LastError   string `json:"last_error,omitempty"`    // why the last attempt failed, "" when it did not
	LastSize    int64  `json:"last_size,omitempty"`     // bytes of the last file sent
	LastOKDay   int64  `json:"last_ok_day,omitempty"`   // unix day of LastOK
	LastTryHour int64  `json:"last_try_hour,omitempty"` // unix hour of LastTry
}

// Errors the admin sees.
var (
	ErrNoChat     = errors.New("no_admin_chat")      // no admin chat connected to the bot
	ErrNoPassword = errors.New("no_backup_password") // no password to encrypt with
	ErrTooBig     = errors.New("backup_too_big")     // over the 50 MB a bot may send
	ErrBusy       = errors.New("backup_busy")        // one is being made right now
)

// Bot is the part of the Telegram bot the backups use (tgbot.Bot).
type Bot interface {
	InfrastructureClient(ctx context.Context) (*tgbot.Client, error)
	InfrastructureAdminChat(ctx context.Context) (int64, bool, error)
}

type Service struct {
	db      *sql.DB
	set     *settings.Settings
	bot     Bot
	dataDir string
	name    func(ctx context.Context) string // the server's name in the file name and caption
	now     func() time.Time
	log     *slog.Logger
	mu      sync.Mutex
}

// New: dataDir is the database's directory, where the copy is made before it is packed.
func New(db *sql.DB, set *settings.Settings, bot Bot, dataDir string, name func(ctx context.Context) string, now func() time.Time, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, set: set, bot: bot, dataDir: dataDir, name: name, now: now, log: log}
}

// Run sends the day's backup at the chosen hour; a failed one is tried again each hour of
// that day.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if s.due(ctx) {
			if _, err := s.Send(ctx); err != nil && !errors.Is(err, ErrBusy) {
				s.log.Warn("telegram backup", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) due(ctx context.Context) bool {
	on, err := s.set.On(ctx, Enabled)
	if err != nil || !on {
		return false
	}
	hour, ok, err := settings.Get[int](ctx, s.set, KeyHour)
	if err != nil {
		return false
	}
	if !ok {
		hour = DefaultHour
	}
	st, _, err := settings.Get[State](ctx, s.set, KeyState)
	if err != nil {
		return false
	}
	now := s.now().UTC()
	day, h := now.Unix()/86400, now.Unix()/3600
	return now.Hour() >= hour && st.LastOKDay < day && st.LastTryHour < h
}

// Send makes a backup and sends it now.
func (s *Service) Send(ctx context.Context) (State, error) {
	if !s.mu.TryLock() {
		return State{}, ErrBusy
	}
	defer s.mu.Unlock()
	st, _, err := settings.Get[State](ctx, s.set, KeyState)
	if err != nil {
		return st, err
	}
	now := s.now().UTC()
	st.LastTry, st.LastTryHour = now.Unix(), now.Unix()/3600
	size, err := s.send(ctx, now)
	st.LastError = ""
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastOK, st.LastOKDay, st.LastSize = now.Unix(), now.Unix()/86400, size
	}
	if serr := settings.Set(ctx, s.set, KeyState, st); serr != nil && err == nil {
		err = serr
	}
	return st, err
}

func (s *Service) send(ctx context.Context, now time.Time) (int64, error) {
	password, err := s.set.String(ctx, KeyPassword)
	if err != nil {
		return 0, err
	}
	if len(password) < MinPassword {
		return 0, ErrNoPassword
	}
	chat, ok, err := s.bot.InfrastructureAdminChat(ctx)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNoChat
	}
	client, err := s.bot.InfrastructureClient(ctx)
	if err != nil {
		return 0, err
	}
	file, err := Make(ctx, s.db, s.dataDir, password, now)
	if err != nil {
		return 0, err
	}
	if len(file) > tgbot.MaxDocument {
		return 0, ErrTooBig
	}
	host := safeName(s.name(ctx))
	name := "mikan-" + host + "-" + now.Format("20060102-1504") + ".tar.gz.age"
	caption := fmt.Sprintf("mikan · %s · %s UTC · %s\n\nage -d -o mikan.tar.gz %s\nmikan restore mikan.tar.gz",
		host, now.Format("2006-01-02 15:04"), sizeText(int64(len(file))), name)
	if _, err := client.SendDocument(ctx, chat, name, file, caption); err != nil {
		return 0, err
	}
	return int64(len(file)), nil
}

// Make writes a consistent copy of the database next to it, packs it as
// data/panel/backup.db (where `mikan restore` looks for it) and encrypts the archive.
func Make(ctx context.Context, db *sql.DB, dir, password string, now time.Time) ([]byte, error) {
	tmp, err := os.CreateTemp(dir, ".telegram-backup-*.db")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	defer os.Remove(path)
	// VACUUM INTO takes an existing file only when it is empty; the copy holds every
	// secret of the panel, so it is private from the start.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return nil, fmt.Errorf("copy the database: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := age.NewScryptRecipient(password)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc, err := age.Encrypt(&out, r)
	if err != nil {
		return nil, err
	}
	if err := pack(enc, data, now); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// pack writes a tar.gz with the directories data and data/panel and the database in it.
func pack(w io.Writer, db []byte, mtime time.Time) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, d := range []string{"data/", "data/panel/"} {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o700, ModTime: mtime}); err != nil {
			return err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "data/panel/backup.db", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(db)), ModTime: mtime}); err != nil {
		return err
	}
	if _, err := tw.Write(db); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// safeName keeps letters, digits, dots and dashes of a host for a file name.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		case r == ':':
			return '-'
		}
		return -1
	}, s)
	if s == "" {
		return "panel"
	}
	return s
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
