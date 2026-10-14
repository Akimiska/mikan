// Package support is the panel's "support access": a temporary, revocable access the admin
// opens for whoever helps them, so that they need to give out neither the password nor SSH.
//
// A grant has a level (view, change, terminal), a lifetime of 2, 6 or 24 hours and a token
// (ms_ + 40 base62) shown once; only its hash is kept. One grant is live at a time. The
// operator uses it in a browser (one entry per link, a session bound to the grant) or with
// "Authorization: Bearer ms_..." (for as long as the grant lives). The API layer (internal/
// panel/api, support.go) decides what a grant lets through; this package keeps the grants,
// their journal lines and the admin's notices.
package support

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/audit"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

const (
	TokenPrefix = "ms_"
	TokenLen    = 40 // base62 characters after the prefix: ~238 bits

	LevelView     = "view"
	LevelChange   = "change"
	LevelTerminal = "terminal"

	// TargetPanel is the panel's own server in terminal_targets; a node is "node:<id>".
	TargetPanel = "panel"

	MaxNote    = 200
	MaxTargets = 64

	// touchEvery spaces the last-used updates of one grant.
	touchEvery = time.Minute
	// keepEnded is how long a grant that ended stays in the table.
	keepEnded = 90 * 24 * time.Hour
)

// Hours are the lifetimes a grant may have.
var Hours = []int{2, 6, 24}

var (
	// ErrInvalid: no such grant, or it was revoked or has expired. The caller must not tell which.
	ErrInvalid = errors.New("support grant invalid")
	// ErrUsed: the link's one browser entry is spent.
	ErrUsed = errors.New("support link already used")
	// ErrNotLive: nothing to revoke, the grant had ended already.
	ErrNotLive = errors.New("support grant not live")
)

// FieldError names the field of a request that is wrong and the code the web shows.
type FieldError struct{ Field, Code string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Code }

// Notice is what the admin is told in Telegram: the operator came or the access ended.
type Notice struct {
	Leave bool // false: the operator entered
	Grant db.SupportGrant
	// Reason of a leave: "revoked", "replaced" or "expired"; of a terminal session's end, what
	// the host's agent says (closed, idle, expired, revoked, exit, recording, error).
	Reason string
	// Terminal is the server ("panel", "node:<id>") when the notice is about a terminal
	// session that began (Leave false) or ended (Leave true) and not about the access;
	// Seconds is how long the ended session lasted.
	Terminal string
	Seconds  int64
}

type Service struct {
	st  *store.Store
	now func() time.Time
	log *slog.Logger

	notify func(ctx context.Context, n Notice)
	ended  func(grantID int64)
}

func New(st *store.Store, now func() time.Time, log *slog.Logger) *Service {
	return &Service{st: st, now: now, log: log}
}

// SetNotifier sets who tells the admin of an operator coming and going (the panel's Telegram
// bot); nil: nobody. Call it before the service is used.
func (s *Service) SetNotifier(fn func(ctx context.Context, n Notice)) { s.notify = fn }

// SetEndedHook sets what runs after a grant ended, by revoke, replacement or expiry, with its
// id: whatever holds something open for the grant (a terminal) lets it go. Call it before
// the service is used.
func (s *Service) SetEndedHook(fn func(grantID int64)) { s.ended = fn }

// NotifyTerminal tells the admin a terminal session of the grant began (leave false) or ended
// (leave true; reason, how it ended, and its length in seconds). Nobody is told when no
// notifier is set.
func (s *Service) NotifyTerminal(ctx context.Context, g db.SupportGrant, target string, leave bool, reason string, seconds int64) {
	if s.notify != nil {
		s.notify(ctx, Notice{Leave: leave, Grant: g, Reason: reason, Terminal: target, Seconds: seconds})
	}
}

// NewToken makes a token.
func NewToken() string { return TokenPrefix + secure.Token(TokenLen) }

// HashToken is what is stored of a token.
func HashToken(token string) string { return secure.SHA256Hex(token) }

// ValidToken is the shape of a token: checked before the database is asked.
func ValidToken(token string) bool {
	rest, ok := strings.CutPrefix(token, TokenPrefix)
	if !ok || len(rest) != TokenLen {
		return false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// Live reports whether the grant can be used at now.
func Live(g db.SupportGrant, now time.Time) bool {
	return !g.RevokedAt.Valid && now.Unix() < g.ExpiresAt
}

// Expires is when the grant ends.
func Expires(g db.SupportGrant) time.Time { return time.Unix(g.ExpiresAt, 0).UTC() }

// Writes reports whether the level lets the operator change things.
func Writes(level string) bool { return level == LevelChange || level == LevelTerminal }

// TerminalAllowed reports whether the grant lets its operator open a terminal on the target
// ("panel" or "node:<id>"): the level is terminal and the server is one the admin named.
// It does not look at the time or at a revocation: the caller has a live grant.
func TerminalAllowed(g db.SupportGrant, target string) bool {
	return g.Level == LevelTerminal && target != "" && slices.Contains(g.TerminalTargets, target)
}

// NodeTarget is the terminal target of a node.
func NodeTarget(id int64) string { return "node:" + strconv.FormatInt(id, 10) }

// CreateInput is what the admin chose for a new grant.
type CreateInput struct {
	AdminID    int64
	Hours      int
	Level      string
	ShowPeople bool
	Targets    []string // terminal level only
	Note       string
	IP         string
}

type Created struct {
	Grant db.SupportGrant
	Token string // shown once
}

// Create opens a grant and ends the live one (a new grant replaces the old). Terminal
// targets are checked here; the caller has already asked for the password and the code the
// terminal level needs.
func (s *Service) Create(ctx context.Context, in CreateInput) (Created, error) {
	if !slices.Contains(Hours, in.Hours) {
		return Created{}, &FieldError{"hours", "support_hours"}
	}
	targets, err := s.checkTargets(ctx, in.Level, in.Targets)
	if err != nil {
		return Created{}, err
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > MaxNote {
		return Created{}, &FieldError{"note", "note_too_long"}
	}
	token := NewToken()
	var out Created
	var gone []goneGrant
	err = s.st.Tx(ctx, func(q *db.Queries) error {
		// A retried transaction starts clean.
		gone = nil
		now := s.now()
		ended, err := s.endAll(ctx, q, now, in.AdminID, in.IP, "replaced")
		if err != nil {
			return err
		}
		gone = ended
		g, err := q.CreateSupportGrant(ctx, db.CreateSupportGrantParams{
			TokenHash: HashToken(token), AdminID: in.AdminID, Level: in.Level, ShowPeople: in.ShowPeople,
			TerminalTargets: targets, Note: note, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(in.Hours) * time.Hour).Unix(),
		})
		if err != nil {
			return err
		}
		out = Created{Grant: g, Token: token}
		return audit.Write(ctx, q, now, audit.Entry{AdminID: in.AdminID, Action: "support.grant", TargetType: audit.TargetSupportGrant,
			TargetID: strconv.FormatInt(g.ID, 10), IP: in.IP,
			Details: map[string]any{"level": g.Level, "hours": in.Hours, "show_people": g.ShowPeople, "terminal_targets": g.TerminalTargets, "note": g.Note}})
	})
	if err != nil {
		return Created{}, err
	}
	s.announce(ctx, gone)
	return out, nil
}

// checkTargets validates the terminal targets against the level: the terminal level needs at
// least one, the other levels none, and a node must exist. A grant keeps them in the order
// given, once each.
func (s *Service) checkTargets(ctx context.Context, level string, in []string) ([]string, error) {
	switch level {
	case LevelView, LevelChange:
		if len(in) > 0 {
			return nil, &FieldError{"terminal_targets", "terminal_targets_not_allowed"}
		}
		return []string{}, nil
	case LevelTerminal:
	default:
		return nil, &FieldError{"level", "support_level"}
	}
	if len(in) == 0 {
		return nil, &FieldError{"terminal_targets", "terminal_targets_required"}
	}
	if len(in) > MaxTargets {
		return nil, &FieldError{"terminal_targets", "terminal_targets_invalid"}
	}
	out := make([]string, 0, len(in))
	for _, t := range in {
		if slices.Contains(out, t) {
			continue
		}
		if t != TargetPanel {
			rest, ok := strings.CutPrefix(t, "node:")
			id, err := strconv.ParseInt(rest, 10, 64)
			// "node:+5" and "node:05" parse but are not the canonical name of a node.
			if !ok || err != nil || id < 1 || NodeTarget(id) != t {
				return nil, &FieldError{"terminal_targets", "terminal_targets_invalid"}
			}
			if _, err := s.st.Q.GetNode(ctx, id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, &FieldError{"terminal_targets", "terminal_targets_invalid"}
				}
				return nil, err
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// goneGrant is a grant that ended in a transaction, to be told and let go of after it.
type goneGrant struct {
	grant  db.SupportGrant
	reason string
}

// endAll ends in tx what is open: grants that ran out are noted as expired, the live one is
// revoked (reason). The operator's sessions go with each. It changes the database only.
func (s *Service) endAll(ctx context.Context, q *db.Queries, now time.Time, adminID int64, ip, reason string) ([]goneGrant, error) {
	n := sql.NullInt64{Int64: now.Unix(), Valid: true}
	var gone []goneGrant
	expired, err := q.ExpireSupportGrants(ctx, n)
	if err != nil {
		return nil, err
	}
	for _, g := range expired {
		if err := s.writeEnd(ctx, q, now, 0, "", "support.expire", g, "expired"); err != nil {
			return nil, err
		}
		gone = append(gone, goneGrant{g, "expired"})
	}
	live, err := q.LiveSupportGrants(ctx, now.Unix())
	if err != nil {
		return nil, err
	}
	for _, l := range live {
		g, err := q.RevokeSupportGrant(ctx, db.RevokeSupportGrantParams{Now: n, ID: l.ID})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := s.writeEnd(ctx, q, now, adminID, ip, "support.revoke", g, reason); err != nil {
			return nil, err
		}
		gone = append(gone, goneGrant{g, reason})
	}
	return gone, nil
}

// writeEnd writes the journal line of a grant that ended and drops the operator's sessions.
func (s *Service) writeEnd(ctx context.Context, q *db.Queries, now time.Time, adminID int64, ip, action string, g db.SupportGrant, reason string) error {
	if _, err := q.DeleteSupportSessions(ctx, sql.NullInt64{Int64: g.ID, Valid: true}); err != nil {
		return err
	}
	return audit.Write(ctx, q, now, audit.Entry{AdminID: adminID, Action: action, TargetType: audit.TargetSupportGrant,
		TargetID: strconv.FormatInt(g.ID, 10), IP: ip, Details: map[string]any{"reason": reason, "entered": g.OperatorSeenAt.Valid}})
}

// announce lets go of what the ended grants held and tells the admin the operator left. It
// runs after the transaction that ended them committed.
func (s *Service) announce(ctx context.Context, gone []goneGrant) {
	for _, e := range gone {
		if s.ended != nil {
			s.ended(e.grant.ID)
		}
		if s.notify != nil && e.grant.OperatorSeenAt.Valid {
			s.notify(ctx, Notice{Leave: true, Grant: e.grant, Reason: e.reason})
		}
	}
}

// Revoke ends the grant now. ErrNotLive: it had ended already.
func (s *Service) Revoke(ctx context.Context, id, adminID int64, ip string) (db.SupportGrant, error) {
	var gone []goneGrant
	var out db.SupportGrant
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		gone, out = nil, db.SupportGrant{}
		now := s.now()
		// What ran out is noted as expired first, so that the grant is not revoked after its time.
		expired, err := q.ExpireSupportGrants(ctx, sql.NullInt64{Int64: now.Unix(), Valid: true})
		if err != nil {
			return err
		}
		for _, g := range expired {
			if err := s.writeEnd(ctx, q, now, 0, "", "support.expire", g, "expired"); err != nil {
				return err
			}
			gone = append(gone, goneGrant{g, "expired"})
		}
		g, err := q.RevokeSupportGrant(ctx, db.RevokeSupportGrantParams{Now: sql.NullInt64{Int64: now.Unix(), Valid: true}, ID: id})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotLive
		}
		if err != nil {
			return err
		}
		out = g
		if err := s.writeEnd(ctx, q, now, adminID, ip, "support.revoke", g, "revoked"); err != nil {
			return err
		}
		gone = append(gone, goneGrant{g, "revoked"})
		return nil
	})
	if errors.Is(err, ErrNotLive) {
		// The expiry of the other grants stays noted: the transaction rolled back, the next sweep writes it.
		return db.SupportGrant{}, err
	}
	if err != nil {
		return db.SupportGrant{}, err
	}
	s.announce(ctx, gone)
	return out, nil
}

// Sweep notes the grants whose time ran out (the journal line, the notice, the sessions),
// and forgets the ones that ended long ago. It runs on a timer and when a request meets an
// expired grant.
func (s *Service) Sweep(ctx context.Context) error {
	var gone []goneGrant
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		gone = nil
		now := s.now()
		expired, err := q.ExpireSupportGrants(ctx, sql.NullInt64{Int64: now.Unix(), Valid: true})
		if err != nil {
			return err
		}
		for _, g := range expired {
			if err := s.writeEnd(ctx, q, now, 0, "", "support.expire", g, "expired"); err != nil {
				return err
			}
			gone = append(gone, goneGrant{g, "expired"})
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.announce(ctx, gone)
	_, err = s.st.Q.PruneSupportGrants(ctx, sql.NullInt64{Int64: s.now().Add(-keepEnded).Unix(), Valid: true})
	return err
}

// Authenticate finds the live grant a token opens. ErrInvalid covers a token that is
// malformed, unknown, revoked or expired alike.
func (s *Service) Authenticate(ctx context.Context, token string) (db.SupportGrant, error) {
	if !ValidToken(token) {
		return db.SupportGrant{}, ErrInvalid
	}
	g, err := s.st.Q.GetSupportGrantByHash(ctx, HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return db.SupportGrant{}, ErrInvalid
	}
	if err != nil {
		return db.SupportGrant{}, err
	}
	return s.liveOrNote(ctx, g)
}

// Get finds a live grant by id, for a request of a session that belongs to it.
func (s *Service) Get(ctx context.Context, id int64) (db.SupportGrant, error) {
	g, err := s.st.Q.GetSupportGrant(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.SupportGrant{}, ErrInvalid
	}
	if err != nil {
		return db.SupportGrant{}, err
	}
	return s.liveOrNote(ctx, g)
}

func (s *Service) liveOrNote(ctx context.Context, g db.SupportGrant) (db.SupportGrant, error) {
	if Live(g, s.now()) {
		return g, nil
	}
	if !g.RevokedAt.Valid && !g.EndedNotedAt.Valid {
		// Its time ran out and nobody wrote it down yet: do it now, not at the next sweep.
		if err := s.Sweep(ctx); err != nil && s.log != nil {
			s.log.Warn("support sweep", "err", err)
		}
	}
	return db.SupportGrant{}, ErrInvalid
}

// Enter spends the link's one browser entry. ErrUsed: it was spent; ErrInvalid: the grant
// ended meanwhile.
func (s *Service) Enter(ctx context.Context, g db.SupportGrant, ip string) error {
	now := s.now()
	n, err := s.st.Q.EnterSupportGrant(ctx, db.EnterSupportGrantParams{Now: sql.NullInt64{Int64: now.Unix(), Valid: true}, Ip: ip, ID: g.ID})
	if err != nil {
		return err
	}
	if n == 0 {
		cur, err := s.st.Q.GetSupportGrant(ctx, g.ID)
		if err == nil && Live(cur, now) && cur.EnteredAt.Valid {
			return ErrUsed
		}
		return ErrInvalid
	}
	s.write(ctx, audit.Entry{Action: "support.enter", TargetType: audit.TargetSupportGrant, TargetID: strconv.FormatInt(g.ID, 10), IP: ip,
		Details: map[string]any{"via": "browser"}})
	s.seen(ctx, g)
	return nil
}

// Used notes that the operator used the grant (a request of a session or a bearer call):
// the last-used time, and the first time of all the admin is told. via is "browser" or
// "api".
func (s *Service) Used(ctx context.Context, g db.SupportGrant, ip, via string) {
	now := s.now()
	err := s.st.Q.TouchSupportGrant(ctx, db.TouchSupportGrantParams{Now: sql.NullInt64{Int64: now.Unix(), Valid: true}, Ip: ip, ID: g.ID,
		OlderThan: sql.NullInt64{Int64: now.Unix() - int64(touchEvery/time.Second), Valid: true}})
	if err != nil && s.log != nil {
		s.log.Warn("support grant touch", "err", err)
	}
	if g.OperatorSeenAt.Valid {
		return
	}
	if s.seen(ctx, g) && via == "api" {
		s.write(ctx, audit.Entry{Action: "support.enter", TargetType: audit.TargetSupportGrant, TargetID: strconv.FormatInt(g.ID, 10), IP: ip,
			Details: map[string]any{"via": via}})
	}
}

// seen marks the first sign of the operator and tells the admin; false: it was seen before.
func (s *Service) seen(ctx context.Context, g db.SupportGrant) bool {
	n, err := s.st.Q.SeeSupportOperator(ctx, db.SeeSupportOperatorParams{ID: g.ID, OperatorSeenAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true}})
	if err != nil {
		if s.log != nil {
			s.log.Warn("support operator seen", "err", err)
		}
		return false
	}
	if n == 0 {
		return false
	}
	if s.notify != nil {
		g.OperatorSeenAt = sql.NullInt64{Int64: s.now().Unix(), Valid: true}
		s.notify(ctx, Notice{Grant: g})
	}
	return true
}

func (s *Service) write(ctx context.Context, e audit.Entry) {
	if err := audit.Write(ctx, s.st.Q, s.now(), e); err != nil && s.log != nil {
		s.log.Warn("support audit write failed", "action", e.Action, "err", err)
	}
}
