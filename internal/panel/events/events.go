// Package events is the panel's feed of events for addons of protocol 2 (permission events):
// what happened to payments, subscribers, devices and nodes (types.go lists them all), written
// in the same transaction as the change itself, so an event is never lost and never told about
// a change that rolled back.
package events

import (
	"encoding/json"
	"sync"
	"time"
)

// Retention: an event older than this is gone, read or not.
const Retention = 7 * 24 * time.Hour

// Event is one entry of the feed.
type Event struct {
	ID     int64           `json:"id"`
	Type   string          `json:"type"`
	At     time.Time       `json:"at"`
	UserID int64           `json:"user_id,omitempty"`
	TgID   int64           `json:"tg_id,omitempty"`
	Data   json.RawMessage `json:"data"`
}

// Hub wakes readers waiting for events of this process.
type Hub struct {
	mu   sync.Mutex
	wake chan struct{}
}

func NewHub() *Hub { return &Hub{wake: make(chan struct{})} }

// Kick wakes every reader waiting now; it may come before the commit, so a reader that
// finds nothing waits again.
func (h *Hub) Kick() {
	if h == nil {
		return
	}
	h.mu.Lock()
	close(h.wake)
	h.wake = make(chan struct{})
	h.mu.Unlock()
}

// Wait is closed by the next Kick; without a hub it never is.
func (h *Hub) Wait() <-chan struct{} {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wake
}
