package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"mikan/internal/panel/secure"
	"mikan/internal/panel/store/db"
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
}

func NewSessions(q *db.Queries, now func() time.Time) *Sessions {
	return &Sessions{q: q, now: now}
}

// Create returns the raw cookie token; only its SHA-256 is stored.
func (s *Sessions) Create(ctx context.Context, adminID int64, ip, userAgent string) (string, db.Session, error) {
	token := secure.Token(43)
	now := s.now().Unix()
	if len(userAgent) > 256 {
		userAgent = userAgent[:256]
	}
	p := db.CreateSessionParams{
		IDHash:     secure.SHA256Hex(token),
		AdminID:    adminID,
		CsrfToken:  secure.Token(32),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now + int64(MaxTTL/time.Second),
		Ip:         ip,
		UserAgent:  userAgent,
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
		if err := s.q.TouchSession(ctx, db.TouchSessionParams{LastSeenAt: sess.LastSeenAt, IDHash: sess.IDHash}); err != nil {
			return db.Session{}, err
		}
	}
	return sess, nil
}

func (s *Sessions) Revoke(ctx context.Context, token string) error {
	return s.q.DeleteSession(ctx, secure.SHA256Hex(token))
}

func (s *Sessions) Cleanup(ctx context.Context) error {
	now := s.now()
	return s.q.DeleteStaleSessions(ctx, db.DeleteStaleSessionsParams{
		Now:        now.Unix(),
		IdleBefore: now.Add(-IdleTTL).Unix(),
	})
}
