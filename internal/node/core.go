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
	// limit holds the user of an inbound to these: kept out (its open connections are
	// closed), and a traffic budget the core cuts it at (0: none), counted from now when
	// fresh, else from when it was set. Neither: no limit.
	limit(inbound, user string, out bool, budget int64, fresh bool)
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

// limit keeps a user out with an expiry in the past: the core closes its connections and
// refuses new ones on every kind of inbound, whether or not it can change its users on the
// fly (Hysteria2 and Mieru cannot). A fresh budget drops the limit first, which forgets
// what the core counted against the last one, so the new budget never trips on old bytes.
func (citrus) limit(inbound, user string, out bool, budget int64, fresh bool) {
	users := statistic.DefaultManager.Users()
	key := statistic.UserKey{Inbound: inbound, User: user}
	if !out && budget <= 0 {
		users.DeleteLimit(key)
		return
	}
	l := statistic.UserLimit{Inbound: inbound, User: user, Quota: max(budget, 0)}
	if out {
		l.ExpireAt = 1
	}
	if fresh {
		users.DeleteLimit(key)
	}
	_ = users.SetLimit(l)
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
