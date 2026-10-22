package subs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/model"
	"mikan/internal/panel/store/db"
)

// The server groups as the Mikan app (getmikan/MikanApp) shows them: the app asks with
// "groups" in X-Mikan-Features, and the answer carries X-Mikan-Groups beside the profile.
// An app that does not ask gets the same subscription as before, header for header: the
// groups never become fake servers or proxy groups that other apps would show.

const (
	// FeaturesHeader lists, comma-separated, the extensions the Mikan app shows.
	FeaturesHeader = "X-Mikan-Features"
	// GroupsHeader is "base64:" and the JSON of AppGroups.
	GroupsHeader = "X-Mikan-Groups"

	featureGroups = "groups"
	// appGroupsMax is the most X-Mikan-Groups takes. A reverse proxy in front of the panel
	// fails an answer whose headers outgrow its buffer (nginx: 4 or 8 KB for all of them by
	// default), so past this the server names go and the groups' traffic stays.
	appGroupsMax = 1536
)

// AppGroups is the JSON of X-Mikan-Groups. V changes only when an app that reads version 1
// would read the new one wrong; fields are only ever added.
type AppGroups struct {
	V int `json:"v"`
	// ResetsAt is when the traffic of the period starts over, the groups' with the main
	// traffic (unix seconds); absent when it never does.
	ResetsAt int64      `json:"resets_at,omitempty"`
	Groups   []AppGroup `json:"groups"`
}

// AppGroup is one server group of the subscription, in the order the admin panel lists them.
type AppGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Mode: "own" a limit of its own apart from the main traffic, "unlimited" apart from it
	// without a limit, "main" its servers spend the main traffic (the app only names them).
	Mode string `json:"mode" enum:"own,unlimited,main"`
	Used int64  `json:"used,omitempty"` // bytes of the period; not for "main"
	// Limit, Extra and Left are of an own limit alone: the base limit, what is left of the
	// traffic bought or given for the group, and what is left of both.
	Limit *int64 `json:"limit,omitempty"`
	Extra int64  `json:"extra,omitempty"`
	Left  *int64 `json:"left,omitempty"`
	// State is "exhausted" once an own limit has nothing left: its servers are out of the
	// profile until the period starts over or traffic is bought. "active" otherwise.
	State string `json:"state" enum:"active,exhausted"`
	// Proxies are the names of the group's servers in this very profile; null when the names
	// did not fit in the header.
	Proxies []string `json:"proxies"`
}

// wantsFeature says whether the app lists f in X-Mikan-Features.
func wantsFeature(h http.Header, f string) bool {
	for _, v := range h.Values(FeaturesHeader) {
		for _, x := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(x), f) {
				return true
			}
		}
	}
	return false
}

// mikanGroups puts X-Mikan-Groups on the answer to a Mikan app that asked for it: the groups
// of u's subscription prof, whose servers in the profile are ps.
func (h *Handler) mikanGroups(w http.ResponseWriter, r *http.Request, u db.User, prof Profile, ps []proxy) {
	if !wantsFeature(r.Header, featureGroups) {
		return
	}
	g, err := h.appGroups(r.Context(), u, prof, ps)
	if err != nil {
		// The profile still goes out: the app shows the groups of its last fetch.
		h.warn("mikan_groups", "subscription: the server groups for the Mikan app are not read", "err", err)
		return
	}
	if v := g.header(); v != "" {
		w.Header().Set(GroupsHeader, v)
	}
}

func (h *Handler) appGroups(ctx context.Context, u db.User, prof Profile, ps []proxy) (AppGroups, error) {
	rows, err := h.st.Q.ListUserPools(ctx, u.ID)
	if err != nil {
		return AppGroups{}, err
	}
	pools, err := h.st.Q.ListTrafficPools(ctx)
	if err != nil {
		return AppGroups{}, err
	}
	out := AppGroups{V: 1, Groups: appGroupsOf(pools, rows, prof.grants, u.ID, prof.access.Groups(prof.all), ps)}
	if t, ok := domain.NextReset(u, h.now()); ok {
		out.ResetsAt = t.Unix()
	}
	return out, nil
}

// appGroupsOf lists the groups of pools the user reaches (reach: Access.Groups, an
// exhausted one included, which the app must explain) and is not shut out of: an own limit
// always, a group apart without a limit with servers in ps or traffic used, a group on the
// main traffic only with servers in ps to name. rows are the user's modes, left the grants.
func appGroupsOf(pools []db.TrafficPool, rows []db.UserPool, left domain.GrantsLeft, userID int64, reach map[int64]bool, ps []proxy) []AppGroup {
	names := map[int64][]string{}
	for _, x := range ps {
		if x.pool != 0 {
			names[x.pool] = append(names[x.pool], x.name)
		}
	}
	var out []AppGroup
	for _, p := range pools {
		if !reach[p.ID] {
			continue
		}
		row, mode := domain.UserPoolOf(rows, p.ID)
		g := AppGroup{ID: p.ID, Name: p.Name, Mode: mode, State: "active", Proxies: names[p.ID]}
		if g.Proxies == nil {
			g.Proxies = []string{}
		}
		used := row.UsedUp + row.UsedDown
		if mode == domain.PoolOwn && !row.TrafficLimit.Valid {
			mode = domain.PoolUnlimited // a limit of its own with no number: counted apart, never runs out
			g.Mode = mode
		}
		switch mode {
		case domain.PoolClosed:
			continue
		case domain.PoolMain:
			if len(g.Proxies) == 0 {
				continue
			}
		case domain.PoolUnlimited:
			if len(g.Proxies) == 0 && used == 0 {
				continue
			}
			g.Used = used
		case domain.PoolOwn:
			extra := left.Pool(userID, p.ID)
			limit, rest := row.TrafficLimit.Int64, model.TrafficLeft(row.TrafficLimit, used, extra)
			g.Used, g.Limit, g.Extra, g.Left = used, &limit, extra, &rest
			if domain.PoolExhausted(row, extra) {
				g.State = "exhausted"
			}
		}
		out = append(out, g)
	}
	return out
}

// header is the X-Mikan-Groups value of g; "" without groups. Past appGroupsMax the server
// names go, and the groups that are names alone (on the main traffic) with them; then the
// last groups, should a great many still not fit.
func (g AppGroups) header() string {
	if len(g.Groups) == 0 {
		return ""
	}
	encode := func() string {
		b, _ := json.Marshal(g)
		return "base64:" + base64.StdEncoding.EncodeToString(b)
	}
	v := encode()
	if len(v) <= appGroupsMax {
		return v
	}
	var kept []AppGroup
	for _, x := range g.Groups {
		if x.Mode != domain.PoolMain {
			x.Proxies = nil
			kept = append(kept, x)
		}
	}
	g.Groups = kept
	for v = encode(); len(v) > appGroupsMax && len(g.Groups) > 0; v = encode() {
		g.Groups = g.Groups[:len(g.Groups)-1]
	}
	if len(g.Groups) == 0 {
		return ""
	}
	return v
}
