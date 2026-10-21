package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/model"
)

// The bot's screens as an addon see the panel over the Panel API: POST /panel/v2/bot/<method>
// calls one method of Backend or Shop ("Shop.<method>") with its arguments as a JSON array
// (the context left out) and answers its results the same way, or an error by its code
// (model.CodeOf). botpanel.RPCHandler is the panel's side, Remote the addon's: the screens run
// unchanged on either (docs/pipeline/addons.md, section 13, PR 10).

// RPCMaxBody bounds a call and its answer.
const RPCMaxBody = 4 << 20

// RPCError is an error as it crosses: its code (model.CodeOf, or "error") and text.
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Remote is Backend over the Panel API: base is MIKAN_PANEL_URL, token MIKAN_PANEL_TOKEN.
type Remote struct {
	base, token string
	hc          *http.Client
	shop        Shop
}

// NewRemote: hc nil is a client with a minute's timeout.
func NewRemote(base, token string, hc *http.Client) *Remote {
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	r := &Remote{base: strings.TrimRight(base, "/"), token: token, hc: hc}
	r.shop = remoteShop{r}
	return r
}

// call runs method with args and reads its results into outs. An error the panel answers
// with a code comes back as the model's error of that code (model.ErrorOf), so the screens
// tell it apart with errors.Is; any other as an *RPCError.
func (r *Remote) call(ctx context.Context, method string, args []any, outs ...any) error {
	if args == nil {
		args = []any{}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/bot/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return fmt.Errorf("panel: %s: %w", method, err)
	}
	defer resp.Body.Close()
	var ans struct {
		Results []json.RawMessage `json:"results"`
		Error   *RPCError         `json:"error"`
		Code    string            `json:"code"` // the Panel API's own refusal
		Message string            `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, RPCMaxBody)).Decode(&ans); err != nil {
		return fmt.Errorf("panel: %s: HTTP %d", method, resp.StatusCode)
	}
	switch {
	case ans.Error != nil:
		if known := model.ErrorOf(ans.Error.Code); known != nil {
			return known
		}
		return ans.Error
	case ans.Code == "not_talking":
		return fmt.Errorf("panel: %s: %w", method, ErrNotTalking)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("panel: %s: %s (%s)", method, ans.Code, ans.Message)
	case len(ans.Results) != len(outs):
		return fmt.Errorf("panel: %s gave %d results, want %d", method, len(ans.Results), len(outs))
	}
	for i, o := range outs {
		if err := json.Unmarshal(ans.Results[i], o); err != nil {
			return fmt.Errorf("panel: %s: result %d: %w", method, i+1, err)
		}
	}
	return nil
}

// Events reads the panel's feed of the events of the given types, waiting up to wait for
// one to come (GET /events).
func (r *Remote) Events(ctx context.Context, wait time.Duration, types []string) ([]events.Event, error) {
	q := url.Values{"wait": {strconv.Itoa(int(wait / time.Second))}, "types": {strings.Join(types, ",")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/events?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("events: HTTP %d", resp.StatusCode)
	}
	var feed struct {
		Events []events.Event `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, RPCMaxBody)).Decode(&feed); err != nil {
		return nil, err
	}
	return feed.Events, nil
}

// Ack tells the panel an event was handled, so that the feed does not hand it out again
// (POST /events/ack).
func (r *Remote) Ack(ctx context.Context, id int64) error {
	body, _ := json.Marshal(map[string]int64{"id": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/events/ack", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return errors.New("ack: HTTP " + resp.Status)
	}
	return nil
}

var _ Shop = remoteShop{}
