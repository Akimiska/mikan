// Package panelimport brings users over from another panel through its API: Marzban,
// PasarGuard and Remnawave. Each becomes a mikan user on a plan the admin picks, with
// the old panel's limit, traffic used, term and device limit kept.
//
// The links people already have can keep working (subs.Handler.Legacy):
//
//   - Remnawave's link is the user's short UUID, the same every time: it is kept as is.
//   - Marzban and PasarGuard sign a new token for every request, so the tokens people
//     hold were never seen by the API. They are checked as the old panel checked them,
//     with its secret (Verifier); without the secret those links are not taken over,
//     since a token without a checked signature is anybody's for the asking.
package panelimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Kind is the panel users come from.
type Kind string

const (
	Marzban    Kind = "marzban"
	PasarGuard Kind = "pasarguard"
	Remnawave  Kind = "remnawave"
)

// Source is how to reach the old panel. Marzban and PasarGuard take an admin's username
// and password (PasarGuard an API key instead); Remnawave an API token.
type Source struct {
	Kind     Kind
	URL      string // the panel's address, https://panel.example.com
	Username string
	Password string
	Token    string // Remnawave API token, or PasarGuard API key
}

// Status of a user in the old panel, in mikan's words.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLimited  = "limited"
	StatusExpired  = "expired"
	StatusOnHold   = "on_hold" // Marzban and PasarGuard: the term starts at the first connection
)

// User is a user of the old panel.
type User struct {
	Name     string
	SourceID int64  // PasarGuard's user id (its tokens carry it); 0 elsewhere
	Status   string // Status*
	Note     string
	Contact  string // Remnawave's Telegram id or e-mail
	// TrafficLimit in bytes; 0: unlimited.
	TrafficLimit int64
	// Used is the traffic of the current period, Lifetime of all time, in bytes.
	Used, Lifetime int64
	// Expires is when the term ends; zero: never.
	Expires time.Time
	// OnHold is the term an on-hold user gets once it starts.
	OnHold time.Duration
	// DeviceLimit; 0: the plan's.
	DeviceLimit int64
	// Token is a subscription token that is the same every time (Remnawave's short UUID).
	Token string
}

// Errors the admin sees.
var (
	ErrAuth        = errors.New("import_auth")        // the old panel refused the credentials
	ErrUnreachable = errors.New("import_unreachable") // the old panel did not answer
	ErrAnswer      = errors.New("import_bad_answer")  // the answer is not what this panel gives
)

// maxUsers keeps a broken or hostile answer from filling the memory.
const maxUsers = 200_000

// Fetch lists the old panel's users.
func Fetch(ctx context.Context, hc *http.Client, src Source) ([]User, error) {
	src.URL = strings.TrimRight(strings.TrimSpace(src.URL), "/")
	if !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "http://") {
		return nil, fmt.Errorf("%w: the address must start with https://", ErrAnswer)
	}
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	switch src.Kind {
	case Marzban, PasarGuard:
		return fetchMarzban(ctx, hc, src)
	case Remnawave:
		return fetchRemnawave(ctx, hc, src)
	}
	return nil, fmt.Errorf("unknown panel %q", src.Kind)
}

// getJSON does a request and decodes a JSON answer into out.
func getJSON(ctx context.Context, hc *http.Client, req *http.Request, out any) error {
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnreachable
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrAuth
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: HTTP %d from %s", ErrAnswer, resp.StatusCode, req.URL.Path)
	}
	// Big panels answer in pages; one page is never near this.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrAnswer, err)
	}
	return nil
}
