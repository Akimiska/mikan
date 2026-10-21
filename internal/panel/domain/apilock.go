package domain

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
)

// A remote node's API may answer only the panel (nodes.api_lockdown): the panel's own
// addresses, the ones the node saw it come from, and the networks the admin adds here
// (nodes.api_allow), for a panel whose connections leave from an address of its hoster's.

// MaxAPIAllow is how many networks the admin may add to a node's API lock.
const MaxAPIAllow = 16

var (
	// ErrLocalLockdown: the panel's own node is reached over its socket, never over TCP.
	ErrLocalLockdown = errors.New("local_node_lockdown")
	// ErrManyAPIAllow: more networks than MaxAPIAllow.
	ErrManyAPIAllow = errors.New("api_allow_many")
)

// APIAllowError is an entry of the API lock's list that is no address or network.
type APIAllowError struct{ Value string }

func (e *APIAllowError) Error() string { return "api_allow_invalid: " + e.Value }

// NormalizeAPIAllow checks the networks the admin added and writes them the one way: an
// address is its own network (203.0.113.5/32), a network has its host bits cleared, the
// list is sorted and each one is there once. A network of everything (/0) is refused: it
// would leave the lock on and lock nothing.
func NormalizeAPIAllow(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		var p netip.Prefix
		if a, err := netip.ParseAddr(s); err == nil && a.Zone() == "" {
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		} else if q, err := netip.ParsePrefix(s); err == nil && q.Bits() > 0 {
			p = q.Masked()
			if p.Addr().Is4In6() {
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
		}
		if !p.IsValid() || p.Bits() <= 0 {
			return nil, &APIAllowError{Value: s}
		}
		out = append(out, p.String())
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > MaxAPIAllow {
		return nil, ErrManyAPIAllow
	}
	return out, nil
}
