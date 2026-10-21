package tgapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A Bot API that does not answer is given up on after a while: the call is not held for
// the minute a long poll may take.
func TestCallsHaveTheirOwnTimeout(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	c := NewClient(srv.URL, "1:x", nil)
	if c.callTimeout != callTimeout || callTimeout > 20*time.Second {
		t.Fatalf("a call's time: %s", c.callTimeout)
	}
	c.callTimeout = 150 * time.Millisecond
	start := time.Now()
	_, err := c.Send(context.Background(), 1, "hi", nil, false)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("a call that hangs: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("gave up after %s", took)
	}
	if hits.Load() != 1 {
		t.Fatal("the call did not reach the server")
	}
	// A cancelled context stays a cancellation, not an unreachable Telegram.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Send(ctx, 1, "hi", nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	// The pre-checkout answer gets less than other calls: Telegram's ten seconds are short.
	if preCheckoutTimeout >= 10*time.Second {
		t.Fatalf("the pre-checkout answer has %s, Telegram gives ten seconds", preCheckoutTimeout)
	}
}

// The outbox hands out a long queue without copying it for every message.
func TestOutboxQueueIsCheapToDrain(t *testing.T) {
	_, c := newSink(t)
	o := NewOutbox(c, Limits{Total: 1e9, Bulk: 1e9, PerChat: 1e9, Burst: 1e9, Workers: 1, MaxTries: 1}, time.Now, nil, nil)
	const jobs = 60000
	for i := range jobs {
		o.Bulk(int64(i), func(context.Context, *Client) error { return nil }, nil)
	}
	start := time.Now()
	for i := range jobs {
		j, _ := o.next()
		if j == nil || j.chat != int64(i) {
			t.Fatalf("job %d: %+v", i, j)
		}
		o.mu.Lock()
		delete(o.busy, j.chat)
		o.mu.Unlock()
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("draining %d jobs took %s", jobs, took)
	}
	if o.pending(prioBulk) != 0 {
		t.Fatal("the lane is not empty")
	}
}
