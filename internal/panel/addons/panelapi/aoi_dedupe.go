package panelapi

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"mikan/internal/tgapi"
)

// AoiVPN fork: a queued call is sent once. The bot addon gives a call 15 s; the gateway's
// outbox may hold it longer (Telegram out of reach from the node for a while, its own
// retries), and then the addon's client sees a timeout and sends the same call again while
// the first one still goes out: one notice came 4–5 times (22.10.2026). An identical call —
// method, chat, body — that comes while the first is queued waits for that one's answer, and
// one that comes soon after it went gets the same answer. A call that failed on the way
// (not Telegram's refusal) is not kept: its retry is a real retry.

const dedupeKeep = time.Minute

type proxyCall struct {
	done   chan struct{}
	code   int
	answer []byte
	err    error
	at     time.Time
}

type callDedupe struct {
	mu    sync.Mutex
	calls map[string]*proxyCall
}

var proxyCalls = &callDedupe{calls: map[string]*proxyCall{}}

// dedupeKey is per proxy (the server's address): two panels in one process (the tests) do
// not share their calls.
func dedupeKey(owner any, method string, chat int64, body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%p", owner) + "|" + method + "|" + strconv.FormatInt(chat, 10) + "|" + string(sum[:])
}

// join returns the call to wait on and whether the caller must send it.
func (d *callDedupe) join(key string, now time.Time) (*proxyCall, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, c := range d.calls {
		if !c.at.IsZero() && now.Sub(c.at) > dedupeKeep {
			delete(d.calls, k)
		}
	}
	if c, ok := d.calls[key]; ok {
		return c, false
	}
	c := &proxyCall{done: make(chan struct{})}
	d.calls[key] = c
	return c, true
}

// finish records the call's answer for those waiting and those who come soon after.
func (d *callDedupe) finish(key string, c *proxyCall, code int, answer []byte, err error, now time.Time) {
	d.mu.Lock()
	c.code, c.answer, c.err, c.at = code, answer, err, now
	var ae *tgapi.APIError
	if err != nil && !errors.As(err, &ae) {
		delete(d.calls, key) // the way failed: a retry must send again
	}
	d.mu.Unlock()
	close(c.done)
}
