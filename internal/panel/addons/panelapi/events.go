package panelapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

// The feed of events (permission events), read by pulling: an addon with no HTTP server of
// its own (a bot on a framework) reads it the same way as the rest. GET /events gives what
// follows the addon's cursor (or ?after=), waiting up to ?wait= seconds for something new;
// POST /events/ack moves the cursor. Delivery is at least once: an addon dedupes by id.

const (
	maxWait   = 50
	maxEvents = 500
)

func (s *Server) handleEvents() {
	s.Handle(http.MethodGet, "/events", "events", s.eventsList)
	s.Handle(http.MethodPost, "/events/ack", "events", s.eventsAck)
}

func (s *Server) eventsList(w http.ResponseWriter, r *http.Request) {
	c, _ := CallerOf(r.Context())
	q := r.URL.Query()
	num := func(name string, def, lo, hi int) (int, bool) {
		v := q.Get(name)
		if v == "" {
			return def, true
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			Fail(w, http.StatusBadRequest, "bad_request", name+" is "+strconv.Itoa(lo)+" to "+strconv.Itoa(hi))
			return 0, false
		}
		return n, true
	}
	limit, ok := num("limit", 100, 1, maxEvents)
	if !ok {
		return
	}
	wait, ok := num("wait", 0, 0, maxWait)
	if !ok {
		return
	}
	var types []string
	if t := q.Get("types"); t != "" {
		types = strings.Split(t, ",")
	}
	var after int64
	if a := q.Get("after"); a != "" {
		n, err := strconv.ParseInt(a, 10, 64)
		if err != nil || n < 0 {
			Fail(w, http.StatusBadRequest, "bad_request", "after is an event id")
			return
		}
		after = n
	} else {
		cur, err := s.d.Store.Q.GetAddonCursor(r.Context(), c.ID)
		if err != nil {
			s.internal(w, "events cursor", err)
			return
		}
		after = cur
	}
	rows, err := longPoll(r.Context(), s.d.Hub, time.Duration(wait)*time.Second, func() ([]db.AddonEvent, error) {
		return s.d.Store.Q.ListAddonEvents(r.Context(), db.ListAddonEventsParams{After: after, Types: types, Lim: int32(limit)})
	})
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		s.internal(w, "events", err)
		return
	}
	out := struct {
		Events []events.Event `json:"events"`
	}{Events: make([]events.Event, 0, len(rows))}
	for _, e := range rows {
		out.Events = append(out.Events, events.Event{ID: e.ID, Type: e.Type, At: time.Unix(e.CreatedAt, 0).UTC(),
			UserID: e.UserID.Int64, TgID: e.TgID.Int64, Data: json.RawMessage(e.Data)})
	}
	Write(w, http.StatusOK, out)
}

func (s *Server) eventsAck(w http.ResponseWriter, r *http.Request) {
	c, _ := CallerOf(r.Context())
	var in struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ID < 1 {
		Fail(w, http.StatusBadRequest, "bad_request", `the body is {"id": <the last event handled>}`)
		return
	}
	// The cursor only moves forward: a late ack of an older event does not replay the rest.
	if err := s.d.Store.Q.AckAddonEvents(r.Context(), db.AckAddonEventsParams{AddonID: c.ID, LastID: in.ID, UpdatedAt: s.d.Now().Unix()}); err != nil {
		s.internal(w, "events ack", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
