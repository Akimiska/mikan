package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"mikan/internal/panel/audit"
	"mikan/internal/panel/model"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// ReissueRules is how the subscriber may replace their own subscription link: so many
// times a period. A link that leaked is the reason to allow it at all; without a limit
// whoever holds the Mini App could keep the subscription unusable for its owner's other
// devices over and over, and every reissue costs a fresh slot and a push to the nodes.
// The admin reissues outside these rules.
type ReissueRules struct {
	// Off: the subscriber reissues nothing themselves, only the admin does.
	Off bool `json:"off,omitempty" doc:"Подписчик не меняет ссылку сам, только админ"`
	// Limit is how many times the subscriber may reissue in Days; 0: as often as they like.
	Limit int `json:"limit" minimum:"0" maximum:"100" doc:"Сколько раз подписчик может сменить ссылку за days; 0: без ограничения"`
	// Days is the window the limit counts in, sliding: a reissue leaves it Days later.
	Days int `json:"days" minimum:"1" maximum:"365" doc:"За сколько дней считается лимит (скользящее окно)"`
}

// DefaultReissueRules: once a day.
var DefaultReissueRules = ReissueRules{Limit: 1, Days: 1}

const (
	MaxReissueLimit = 100
	MaxReissueDays  = 365
)

// Validate checks rules the admin sends.
func (r ReissueRules) Validate() error {
	switch {
	case r.Limit < 0 || r.Limit > MaxReissueLimit:
		return fieldErr("limit", "bad_reissue_limit")
	case r.Days < 1 || r.Days > MaxReissueDays:
		return fieldErr("days", "bad_reissue_days")
	}
	return nil
}

func (r ReissueRules) window() time.Duration { return time.Duration(r.Days) * 24 * time.Hour }

// LoadReissueRules reads the admin's rules; DefaultReissueRules until they set any. Rules
// stored broken fall back to the default, as LoadUnbindRules does: only a database that
// cannot be read is an error.
func LoadReissueRules(ctx context.Context, set *settings.Settings) (ReissueRules, error) {
	raw, found, err := set.Raw(ctx, settings.KeyReissueRules)
	if err != nil || !found {
		return DefaultReissueRules, err
	}
	r := DefaultReissueRules
	if json.Unmarshal([]byte(raw), &r) != nil || r.Validate() != nil {
		return DefaultReissueRules, nil
	}
	return r, nil
}

// ReissueState is what the subscriber may do now under the rules.
type ReissueState struct {
	Rules ReissueRules
	// Used is how many reissues of theirs the window holds.
	Used int
	// Next is when they may reissue again; zero: now.
	Next time.Time
}

// Left is how many reissues the window still has room for (model.ReissueState.Left).
func (s ReissueState) Left() int { return s.Model().Left() }

// Model is the state as the bot reads it.
func (s ReissueState) Model() model.ReissueState {
	return model.ReissueState{Rules: model.ReissueRules(s.Rules), Used: s.Used, Next: s.Next}
}

var (
	// ErrReissueOff: the admin reissues links, the subscriber does not.
	ErrReissueOff = model.ErrReissueOff
	// ErrReissueCooldown: the subscriber used up the reissues the admin's rules allow for now.
	ErrReissueCooldown = model.ErrReissueCooldown
)

// ReissueState of userID now.
func (s *Users) ReissueState(ctx context.Context, userID int64) (ReissueState, error) {
	rules, err := LoadReissueRules(ctx, s.set)
	if err != nil {
		return ReissueState{Rules: rules}, err
	}
	return reissueState(ctx, s.st.Q, rules, userID, s.now())
}

// reissueState counts the reissues in the window: with Limit of them there, the next comes
// when the oldest that fills it leaves the window.
func reissueState(ctx context.Context, q *db.Queries, rules ReissueRules, userID int64, now time.Time) (ReissueState, error) {
	st := ReissueState{Rules: rules}
	if rules.Off || rules.Limit == 0 {
		return st, nil
	}
	var err error
	st.Used, st.Next, err = sliding(rules.Limit, rules.window(), now, func(since int64) ([]int64, error) {
		return q.ListLinkReissuesSince(ctx, db.ListLinkReissuesSinceParams{UserID: userID, At: since})
	})
	return st, err
}

// sliding counts what list gives of a window of so long that ends now: used is how much, and
// with limit of it there, next is when the oldest that fills it leaves the window (zero:
// now). list gives the times (unix seconds, oldest first) since the window's start. The
// rules of the link's reissues and of the devices' unbinds count alike.
func sliding(limit int, window time.Duration, now time.Time, list func(since int64) ([]int64, error)) (used int, next time.Time, err error) {
	ats, err := list(now.Add(-window).Unix())
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(ats) >= limit {
		next = store.Time(ats[len(ats)-limit]).Add(window)
	}
	return len(ats), next, nil
}

// ReissueByOwner is Reissue done by the subscriber themselves, within the admin's
// ReissueRules (ErrReissueOff, ErrReissueCooldown). via says where from ("bot",
// "miniapp"): the audit journal records it as the subscriber's doing, not an admin's. The
// old link, its slot and every bound device stop working at once, as for the admin.
func (s *Users) ReissueByOwner(ctx context.Context, id int64, via string) (db.User, error) {
	rules, err := LoadReissueRules(ctx, s.set)
	if err != nil {
		return db.User{}, err
	}
	if rules.Off {
		return db.User{}, ErrReissueOff
	}
	err = s.WithSlots(ctx, func() error { return s.reissueByOwner(ctx, rules, id, via) })
	if err != nil {
		return db.User{}, err
	}
	s.hub.Kick() // the devices it unbound
	s.changes.PoliciesChanged()
	return s.Get(ctx, id)
}

func (s *Users) reissueByOwner(ctx context.Context, rules ReissueRules, id int64, via string) error {
	now := s.now()
	return s.st.Tx(ctx, func(q *db.Queries) error {
		// The user first, locked: a double tap waits here and finds the reissue counted,
		// instead of passing the limit twice.
		if _, err := q.LockUser(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		st, err := reissueState(ctx, q, rules, id, now)
		if err != nil {
			return err
		}
		if !st.Next.IsZero() {
			return ErrReissueCooldown
		}
		if err := s.reissueOn(ctx, q, id, now.Unix()); err != nil {
			return err
		}
		if err := q.AddLinkReissue(ctx, db.AddLinkReissueParams{UserID: id, At: now.Unix()}); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{Action: "user.reissue", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
			Details: map[string]any{"actor": audit.ActorSubscriber, "via": via}})
	})
}
