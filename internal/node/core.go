package node

import (
	"strings"
	"time"

	"github.com/metacubex/mihomo/tunnel/statistic"
)

// The core counts every user of every inbound, lists the addresses each is online from,
// turns users away and closes their connections. What the node adds on top is the panel's
// view of a user: one slot across all the inbounds of the node (its devices, its quota,
// its traffic pools, its speed), which the core, keeping inbounds apart, has no notion of.

// core is what the registry uses of the network core.
type core interface {
	// drain returns the traffic of each user of each inbound since the last drain, and
	// who is online now.
	drain() []userStat
	// limits holds the users of inbounds in set to their limits, drops the limits of those
	// in remove, and starts the budgets of those in fresh over (counted from now, not from
	// when they were set): one pass of the core for all the changes the registry makes at
	// once.
	limits(set map[userKey]coreLimit, remove, fresh []userKey)
	// closeUser closes the user's open connections on an inbound.
	closeUser(inbound, user string)
}

type userStat struct {
	Inbound, User string
	Up, Down      int64
	Conns         int64
	IPs           []userIP
}

type userIP struct {
	IP       string
	Conns    int64
	LastSeen time.Time
}

// citrus is the core the node runs.
type citrus struct{}

func (citrus) drain() []userStat {
	stats := statistic.DefaultManager.Users().Drain()
	out := make([]userStat, 0, len(stats))
	for _, st := range stats {
		u := userStat{Inbound: st.Inbound, User: st.User, Up: st.Up, Down: st.Down, Conns: st.Conns}
		if len(st.IPs) > 0 {
			u.IPs = make([]userIP, 0, len(st.IPs))
			for _, ip := range st.IPs {
				u.IPs = append(u.IPs, userIP{IP: ip.IP.Unmap().String(), Conns: ip.Conns, LastSeen: ip.LastSeen})
			}
		}
		out = append(out, u)
	}
	return out
}

// limits keeps a user out with an expiry in the past: the core closes its connections and
// refuses new ones on every kind of inbound, whether or not it can change its users on the
// fly (Hysteria2, Mieru and ShadowQUIC cannot). A speed goes with the user's speed group, so
// the user's slots take from one pair of buckets on every inbound. A fresh budget forgets
// what the core counted against the last one first, so it never trips on old bytes.
func (citrus) limits(set map[userKey]coreLimit, remove, fresh []userKey) {
	users := statistic.DefaultManager.Users()
	if len(fresh) > 0 {
		anew := make(map[statistic.UserKey]bool, len(fresh))
		for _, k := range fresh {
			anew[statistic.UserKey{Inbound: k.inbound, User: k.user}] = true
		}
		users.ResetQuota(func(k statistic.UserKey) bool { return anew[k] })
	}
	ls := make([]statistic.UserLimit, 0, len(set))
	for k, l := range set {
		ul := statistic.UserLimit{Inbound: k.inbound, User: k.user, Quota: max(l.budget, 0), SpeedUp: l.up, SpeedDown: l.down}
		if l.up > 0 || l.down > 0 {
			ul.SpeedGroup = l.group
		}
		if l.out {
			ul.ExpireAt = 1
		}
		ls = append(ls, ul)
	}
	rm := make([]statistic.UserKey, 0, len(remove))
	for _, k := range remove {
		rm = append(rm, statistic.UserKey{Inbound: k.inbound, User: k.user})
	}
	// The core refuses only negative numbers and empty names; the registry makes neither.
	_ = users.SetLimits(ls, rm)
}

func (citrus) closeUser(inbound, user string) {
	statistic.DefaultManager.CloseUserConnections(inbound, user)
}

// coreUser is the name the core knows the slot by on an inbound: TUIC users are their
// UUIDs, in lower case, everyone else the slot name (proto.Listener).
func coreUser(s *slot, byUUID bool) string {
	if byUUID {
		return strings.ToLower(s.uuid)
	}
	return s.name
}
