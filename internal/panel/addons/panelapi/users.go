package panelapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mikan/internal/panel/audit"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

// Subscribers outside the bot. users:read and users:write reach only the accounts the bot is
// talking to (botrpc.go); these two reach every subscriber, so an addon needs them named,
// and the admin is warned of both when approving. users:all reads (a list with search, a
// card); users:grant gives days and traffic, each grant under a key of the addon's own so
// a retry never gives twice.

const (
	maxUsersPage = 100
	defUsersPage = 50
	maxSearch    = 100
	maxReason    = 200 // runes: the grant note's bound
	scanBatch    = 500 // users read at a time while a search looks for a page's worth
)

// validGrantKey: what an addon names a grant by, so keys stay short and plain.
var validGrantKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

func (s *Server) handleUsers() {
	s.Handle(http.MethodGet, "/users", "users:all", s.usersList)
	s.Handle(http.MethodGet, "/users/{id}", "users:all", s.userGet)
	s.Handle(http.MethodPost, "/users/{id}/grant", "users:grant", s.userGrant)
}

// userBrief is a subscriber in a list. It never carries the subscription link, the note or
// the contact: a link is the user's credential, the rest is the admin's.
type userBrief struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	State      string     `json:"state"` // active, expiring, limited, expired, disabled
	TariffID   *int64     `json:"tariff_id"`
	TariffName string     `json:"tariff_name"`
	ExpiresAt  *time.Time `json:"expires_at"` // null: never
	TgID       *int64     `json:"tg_id"`
}

type trafficView struct {
	Used  int64  `json:"used"`
	Limit *int64 `json:"limit"` // null: unlimited
	Left  *int64 `json:"left"`  // what is left of the limit and the packages; null: unlimited
}

type tgLinkView struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// userCard is one subscriber in full.
type userCard struct {
	userBrief
	Traffic      trafficView  `json:"traffic"`
	PackagesLeft int64        `json:"packages_left"` // bytes left of the traffic packages of the main limit
	Devices      int64        `json:"devices"`       // devices bound to the subscription
	TgLinks      []tgLinkView `json:"tg_links"`
}

func unixPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

func idPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// tariffNames reads each tariff's name once per response; an archived or deleted one is "".
type tariffNames struct {
	q     *db.Queries
	names map[int64]string
}

func (t *tariffNames) of(ctx context.Context, id sql.NullInt64) (string, error) {
	if !id.Valid {
		return "", nil
	}
	if n, ok := t.names[id.Int64]; ok {
		return n, nil
	}
	tf, err := t.q.GetTariff(ctx, id.Int64)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if t.names == nil {
		t.names = map[int64]string{}
	}
	t.names[id.Int64] = tf.Name
	return tf.Name, nil
}

func (s *Server) brief(ctx context.Context, u db.User, grants domain.GrantsLeft, tariffs *tariffNames) (userBrief, error) {
	name, err := tariffs.of(ctx, u.TariffID)
	if err != nil {
		return userBrief{}, err
	}
	return userBrief{ID: u.ID, Name: u.Name, State: domain.State(u, grants.Main(u.ID), s.d.Now()), TariffID: idPtr(u.TariffID),
		TariffName: name, ExpiresAt: unixPtr(u.ExpiresAt)}, nil
}

// usersList pages by id, oldest first, from ?after= (exclusive), so a page never moves
// when subscribers come and go; "next" is the cursor of the following page, null at the end.
// ?q= matches a name (a part of it, any case), an id, or a Telegram id the subscription
// is linked to.
func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()
	limit, ok := intParam(query.Get("limit"), defUsersPage, 1, maxUsersPage)
	if !ok {
		Fail(w, http.StatusBadRequest, "bad_request", "limit is 1 to 100")
		return
	}
	after, ok := intParam(query.Get("after"), 0, 0, math.MaxInt64)
	if !ok {
		Fail(w, http.StatusBadRequest, "bad_request", "after is a user id")
		return
	}
	needle := strings.TrimSpace(query.Get("q"))
	if utf8.RuneCountInString(needle) > maxSearch {
		Fail(w, http.StatusBadRequest, "bad_request", "q is at most 100 characters")
		return
	}
	match, err := s.userMatcher(ctx, needle)
	if err != nil {
		s.internal(w, "users search", err)
		return
	}
	// A search reads in big batches and filters here (the search lowercases as Go does, which
	// PostgreSQL's lower() does not for every locale); without one a page is one read.
	batch := int32(limit + 1)
	if needle != "" {
		batch = scanBatch
	}
	var page []db.User
	cursor := after
	for int64(len(page)) <= limit {
		rows, err := s.d.Store.Q.ListUsersAfter(ctx, db.ListUsersAfterParams{After: cursor, Lim: batch})
		if err != nil {
			s.internal(w, "users list", err)
			return
		}
		for _, u := range rows {
			cursor = u.ID
			if match(u) {
				page = append(page, u)
				if int64(len(page)) > limit {
					break
				}
			}
		}
		if len(rows) < int(batch) {
			break
		}
	}
	out := struct {
		Users []userBrief `json:"users"`
		Next  *int64      `json:"next"`
	}{Users: make([]userBrief, 0, len(page))}
	if int64(len(page)) > limit {
		page = page[:limit]
		out.Next = &page[len(page)-1].ID
	}
	ids := make([]int64, len(page))
	for i, u := range page {
		ids[i] = u.ID
	}
	grants, err := domain.GrantsLeftOf(ctx, s.d.Store.Q, ids, s.d.Now())
	if err != nil {
		s.internal(w, "users grants", err)
		return
	}
	links, err := s.d.Store.Q.ListTgLinksOfUsers(ctx, ids)
	if err != nil {
		s.internal(w, "users links", err)
		return
	}
	tg := make(map[int64]int64, len(links))
	for _, l := range links {
		tg[l.UserID] = l.TgID
	}
	tariffs := &tariffNames{q: s.d.Store.Q}
	for _, u := range page {
		b, err := s.brief(ctx, u, grants, tariffs)
		if err != nil {
			s.internal(w, "users tariff", err)
			return
		}
		if id, ok := tg[u.ID]; ok {
			b.TgID = &id
		}
		out.Users = append(out.Users, b)
	}
	Write(w, http.StatusOK, out)
}

// userMatcher is the filter of a search: everything for an empty one.
func (s *Server) userMatcher(ctx context.Context, needle string) (func(db.User) bool, error) {
	if needle == "" {
		return func(db.User) bool { return true }, nil
	}
	lower := strings.ToLower(needle)
	n, numeric := int64(0), false
	linked := map[int64]bool{}
	if v, err := strconv.ParseInt(needle, 10, 64); err == nil && v > 0 {
		n, numeric = v, true
		owned, err := s.d.Store.Q.ListTgLinksOf(ctx, v)
		if err != nil {
			return nil, err
		}
		for _, u := range owned {
			linked[u.ID] = true
		}
	}
	return func(u db.User) bool {
		return strings.Contains(strings.ToLower(u.Name), lower) || numeric && (u.ID == n || linked[u.ID])
	}, nil
}

func userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		Fail(w, http.StatusBadRequest, "bad_request", "the id is a user id")
		return 0, false
	}
	return id, true
}

func (s *Server) userGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := userID(w, r)
	if !ok {
		return
	}
	u, err := s.d.Users.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		Fail(w, http.StatusNotFound, "not_found", "no such user")
		return
	}
	if err != nil {
		s.internal(w, "user", err)
		return
	}
	q := s.d.Store.Q
	grants, err := domain.UserGrantsLeft(ctx, q, id, s.d.Now())
	if err != nil {
		s.internal(w, "user grants", err)
		return
	}
	bound, err := q.CountBoundDevices(ctx, id)
	if err != nil {
		s.internal(w, "user devices", err)
		return
	}
	b, err := s.brief(ctx, u, grants, &tariffNames{q: q})
	if err != nil {
		s.internal(w, "user tariff", err)
		return
	}
	card := userCard{userBrief: b, PackagesLeft: grants.Main(id), Devices: bound, TgLinks: []tgLinkView{}}
	card.Traffic = trafficView{Used: u.UsedUp + u.UsedDown, Limit: idPtr(u.TrafficLimit)}
	if left := domain.TrafficLeft(u.TrafficLimit, card.Traffic.Used, grants.Main(id)); left >= 0 {
		card.Traffic.Left = &left
	}
	switch l, err := q.TgLinkOfUser(ctx, id); {
	case err == nil:
		card.TgID = &l.TgID
		card.TgLinks = append(card.TgLinks, tgLinkView{ID: l.TgID, Username: l.Username, Name: l.FirstName})
	case !errors.Is(err, sql.ErrNoRows):
		s.internal(w, "user link", err)
		return
	}
	Write(w, http.StatusOK, card)
}

// grantRequest is the body of a grant. Days and bytes are pointers: an explicit 0 is a
// mistake, not "none".
type grantRequest struct {
	Days     *int64 `json:"days"`
	Bytes    *int64 `json:"bytes"`
	PoolID   *int64 `json:"pool_id"`
	Lifetime string `json:"lifetime"`
	Reason   string `json:"reason"`
	Key      string `json:"key"`
}

// grantResult is what a grant answers, kept under its key: a repeat answers the same,
// with repeat set.
type grantResult struct {
	UserID         int64      `json:"user_id"`
	Days           int64      `json:"days,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"` // the end of the term after the days
	Bytes          int64      `json:"bytes,omitempty"`
	PoolID         *int64     `json:"pool_id,omitempty"`
	Lifetime       string     `json:"lifetime,omitempty"`
	GrantID        int64      `json:"grant_id,omitempty"`
	TrafficExpires *time.Time `json:"traffic_expires_at,omitempty"`
	Repeat         bool       `json:"repeat"`
}

// Why a grant stopped inside its transaction, told apart from a failure of the database.
var (
	errNoUser       = errors.New("no such user")
	errKeyOtherUser = errors.New("the key was used for another user")
	errUserDisabled = errors.New("the user is disabled")
)

// userGrant gives days (an extension of the term) and/or traffic. Days turn a disabled
// user on, as the admin's extension does; that is the admin's call, so an addon is refused
// for a disabled user (409 user_disabled). Everything is one transaction: the key, the
// days, the traffic, the audit lines and the event are all there or none is.
func (s *Server) userGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, _ := CallerOf(ctx)
	id, ok := userID(w, r)
	if !ok {
		return
	}
	var in grantRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		Fail(w, http.StatusBadRequest, "bad_request", "the body is JSON: days, bytes, pool_id, lifetime, reason, key")
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case !validGrantKey.MatchString(in.Key):
		Fail(w, http.StatusBadRequest, "bad_request", "key is 1 to 64 of A-Z a-z 0-9 . _ : -")
		return
	case in.Reason == "" || utf8.RuneCountInString(in.Reason) > maxReason:
		Fail(w, http.StatusBadRequest, "bad_request", "reason is 1 to 200 characters")
		return
	case in.Days == nil && in.Bytes == nil:
		Fail(w, http.StatusBadRequest, "bad_request", "days or bytes is needed")
		return
	case in.Bytes == nil && (in.PoolID != nil || in.Lifetime != ""):
		Fail(w, http.StatusBadRequest, "bad_request", "pool_id and lifetime go with bytes")
		return
	case in.PoolID != nil && *in.PoolID < 1:
		Fail(w, http.StatusBadRequest, "bad_request", "pool_id is a pool id")
		return
	}
	// Traffic that does not say how long it lasts lasts until it is used up.
	if in.Bytes != nil && in.Lifetime == "" {
		in.Lifetime = domain.LifetimeUsed
	}
	var days, bytes, pool int64
	if in.Days != nil {
		days = *in.Days
	}
	if in.Bytes != nil {
		bytes = *in.Bytes
	}
	if in.PoolID != nil {
		pool = *in.PoolID
	}

	var res grantResult
	var repeat bool
	err := s.d.Store.Tx(ctx, func(q *db.Queries) error {
		// A retried transaction starts clean.
		res, repeat = grantResult{UserID: id}, false
		u, err := q.GetUser(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errNoUser
		}
		if err != nil {
			return err
		}
		now := s.d.Now()
		// The key first: a repeat changes nothing, and two requests with one key never both
		// get past it (the second waits for the first's commit, then finds the key).
		n, err := q.ClaimAddonGrantKey(ctx, db.ClaimAddonGrantKeyParams{AddonID: c.ID, Key: in.Key, UserID: id, CreatedAt: now.Unix()})
		if err != nil {
			return err
		}
		if n == 0 {
			first, err := q.GetAddonGrantKey(ctx, db.GetAddonGrantKeyParams{AddonID: c.ID, Key: in.Key})
			if err != nil {
				return err
			}
			if first.UserID != id {
				return errKeyOtherUser
			}
			repeat = true
			return json.Unmarshal([]byte(first.Result), &res)
		}
		actor := audit.AddonActor(c.ID)
		if in.Days != nil {
			if u.Status == "disabled" {
				return errUserDisabled
			}
			ext, err := s.d.Users.ExtendOn(ctx, q, id, days)
			if err != nil {
				return err
			}
			res.Days, res.ExpiresAt = days, unixPtr(ext.ExpiresAt)
			err = audit.Write(ctx, q, now, audit.Entry{Action: "user.extend", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
				Details: map[string]any{"actor": actor, "days": days, "reason": in.Reason, "key": in.Key}})
			if err != nil {
				return err
			}
		}
		if in.Bytes != nil {
			g, err := s.d.Users.GrantOn(ctx, q, id, domain.GrantInput{PoolID: pool, Bytes: bytes, Lifetime: in.Lifetime, Days: days, Note: in.Reason})
			if err != nil {
				return err
			}
			res.Bytes, res.PoolID, res.Lifetime, res.GrantID, res.TrafficExpires = g.Bytes, in.PoolID, g.Lifetime, g.ID, unixPtr(g.ExpiresAt)
			err = audit.Write(ctx, q, now, audit.Entry{Action: "user.grant", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
				Details: map[string]any{"actor": actor, "bytes": g.Bytes, "pool_id": in.PoolID, "lifetime": g.Lifetime, "reason": in.Reason, "key": in.Key}})
			if err != nil {
				return err
			}
		}
		stored, err := json.Marshal(res)
		if err != nil {
			return err
		}
		if err := q.SetAddonGrantResult(ctx, db.SetAddonGrantResultParams{AddonID: c.ID, Key: in.Key, Result: string(stored)}); err != nil {
			return err
		}
		var tg int64
		switch l, err := q.GetTgLink(ctx, id); {
		case err == nil:
			tg = l.TgID
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		return events.Emit(ctx, q, now, events.UserGranted, id, tg, map[string]any{
			"user_id": id, "days": days, "bytes": bytes, "pool_id": in.PoolID, "reason": in.Reason, "addon": c.ID,
		}, "user.granted:"+c.ID+":"+in.Key)
	})
	var fe *domain.FieldError
	switch {
	case errors.Is(err, errNoUser), errors.Is(err, domain.ErrNotFound):
		Fail(w, http.StatusNotFound, "not_found", "no such user")
		return
	case errors.Is(err, errKeyOtherUser):
		Fail(w, http.StatusConflict, "conflict", "the key was used for another user")
		return
	case errors.Is(err, errUserDisabled):
		Fail(w, http.StatusConflict, "user_disabled", "days are not given to a disabled user")
		return
	case errors.As(err, &fe):
		Fail(w, http.StatusBadRequest, "bad_request", fe.Field+": "+fe.Code)
		return
	case err != nil:
		s.internal(w, "grant", err)
		return
	}
	if !repeat {
		s.d.Users.Changed()
		s.d.Hub.Kick()
	}
	res.Repeat = repeat
	Write(w, http.StatusOK, res)
}
