package panelapi

import (
	"errors"
	"testing"
	"time"

	"mikan/internal/tgapi"
)

func TestCallDedupe(t *testing.T) {
	d := &callDedupe{calls: map[string]*proxyCall{}}
	now := time.Unix(1_000_000, 0)
	c1, send := d.join("k", now)
	if !send {
		t.Fatal("the first call must be sent")
	}
	c2, send := d.join("k", now.Add(5*time.Second))
	if send || c2 != c1 {
		t.Fatal("an identical call while the first is queued must wait for it")
	}
	d.finish("k", c1, 200, []byte(`{"ok":true}`), nil, now.Add(20*time.Second))
	<-c2.done
	if c3, send := d.join("k", now.Add(40*time.Second)); send || c3.code != 200 {
		t.Fatal("an identical call soon after must get the same answer")
	}
	if _, send := d.join("k", now.Add(20*time.Second+2*time.Minute)); !send {
		t.Fatal("after a minute the call is sent again")
	}
	// A call that failed on the way is not kept: its retry sends again.
	c4, _ := d.join("net", now)
	d.finish("net", c4, 0, nil, errors.New("timeout"), now)
	if _, send := d.join("net", now.Add(time.Second)); !send {
		t.Fatal("a failed call must be retried")
	}
	// Telegram's own refusal is an answer: kept like a success.
	c5, _ := d.join("api", now)
	d.finish("api", c5, 400, []byte(`{"ok":false}`), &tgapi.APIError{Code: 400}, now)
	if _, send := d.join("api", now.Add(time.Second)); send {
		t.Fatal("Telegram's refusal must not be sent again")
	}
}
