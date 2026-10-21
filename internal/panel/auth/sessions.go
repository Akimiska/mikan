package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"mikan/internal/panel/secure"
	"mikan/internal/panel/store/db"
	"mikan/internal/textutil"
)

const (
	CookieName = "__Host-mikan"
	IdleTTL    = 12 * time.Hour
	MaxTTL     = 7 * 24 * time.Hour
	// last_seen_at is written at most this often to avoid a DB write on every request.
	touchEvery = time.Minute
)

var ErrNoSession = errors.New("no valid session")

type Sessions struct {
	q   *db.Queries
	now func() time.Time
	log *slog.Logger
}

// NewSessions: log may be nil, it only hears of a last-seen time that could not be saved.
func NewSessions(q *db.Queries, now func() time.Time, log *slog.Logger) *Sessions {
	return &Sessions{q: q, now: now, log: log}
}

// Create returns the raw cookie token; only its SHA-256 is stored.
func (s *Sessions) Create(ctx context.Context, adminID int64, ip, userAgent string) (string, db.Session, error) {
	return s.create(ctx, adminID, sql.NullInt64{}, 0, ip, userAgent)
}

// CreateSupport makes the session of a support operator for a grant: it acts for the
// admin who opened the grant and ends with the grant, at expires (unix seconds) at the
// latest.
func (s *Sessions) CreateSupport(ctx context.Context, adminID, grantID, expires int64, ip, userAgent string) (string, db.Session, error) {
	return s.create(ctx, adminID, sql.NullInt64{Int64: grantID, Valid: true}, expires, ip, userAgent)
}

func (s *Sessions) create(ctx context.Context, adminID int64, grant sql.NullInt64, expires int64, ip, userAgent string) (string, db.Session, error) {
	token := secure.Token(43)
	now := s.now().Unix()
	// The header is the client's: a cut character or bytes that are not UTF-8 would make the
	// row unwritable and the sign-in fail.
	userAgent = textutil.Clip(userAgent, 256)
	end := now + int64(MaxTTL/time.Second)
	if expires > 0 {
		end = min(end, expires)
	}
	p := db.CreateSessionParams{
		IDHash:         secure.SHA256Hex(token),
		AdminID:        adminID,
		CsrfToken:      secure.Token(32),
		CreatedAt:      now,
		LastSeenAt:     now,
		ExpiresAt:      end,
		Ip:             ip,
		UserAgent:      userAgent,
		SupportGrantID: grant,
	}
	if err := s.q.CreateSession(ctx, p); err != nil {
		return "", db.Session{}, err
	}
	return token, db.Session(p), nil
}

func (s *Sessions) Lookup(ctx context.Context, token string) (db.Session, error) {
	if token == "" {
		return db.Session{}, ErrNoSession
	}
	sess, err := s.q.GetSession(ctx, secure.SHA256Hex(token))
	if errors.Is(err, sql.ErrNoRows) {
		return db.Session{}, ErrNoSession
	}
	if err != nil {
		return db.Session{}, err
	}
	now := s.now()
	if now.Unix() >= sess.ExpiresAt || now.Sub(time.Unix(sess.LastSeenAt, 0)) > IdleTTL {
		_ = s.q.DeleteSession(ctx, sess.IDHash)
		return db.Session{}, ErrNoSession
	}
	if now.Sub(time.Unix(sess.LastSeenAt, 0)) > touchEvery {
		sess.LastSeenAt = now.Unix()
		// The session is valid; failing to note that it was seen (a busy database) must not
		// fail the request. The next one notes it.
		if err := s.q.TouchSession(ctx, db.TouchSessionParams{LastSeenAt: sess.LastSeenAt, IDHash: sess.IDHash}); err != nil && s.log != nil {
			s.log.Warn("session last seen not saved", "err", err)
		}
	}
	return sess, nil
}

func (s *Sessions) Cleanup(ctx context.Context) error {
	now := s.now()
	return s.q.DeleteStaleSessions(ctx, db.DeleteStaleSessionsParams{
		Now:        now.Unix(),
		IdleBefore: now.Add(-IdleTTL).Unix(),
	})
}
