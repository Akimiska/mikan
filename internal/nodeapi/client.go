package nodeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Client talks to a node over its unix socket.
type Client struct {
	hc *http.Client
}

func NewUnixClient(socket string) *Client {
	return &Client{hc: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
		},
		MaxIdleConns: 4,
	}}}
}

var ErrUnavailable = errors.New("node unavailable")

func (c *Client) do(ctx context.Context, method, path string, in, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://node"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e Error
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e) == nil && e.Code != "" {
			return &e
		}
		return fmt.Errorf("node %s %s: status %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Apply(ctx context.Context, s DesiredState) (ApplyResult, error) {
	var r ApplyResult
	err := c.do(ctx, http.MethodPut, "/v1/state", s, &r, 60*time.Second)
	return r, err
}

func (c *Client) SetPolicies(ctx context.Context, epoch string, p []Policy) error {
	return c.do(ctx, http.MethodPut, "/v1/policies", PoliciesRequest{Epoch: epoch, Policies: p}, nil, 30*time.Second)
}

func (c *Client) Kick(ctx context.Context, slots []string) error {
	return c.do(ctx, http.MethodPost, "/v1/kick", KickRequest{Slots: slots}, nil, 10*time.Second)
}

func (c *Client) Counters(ctx context.Context) (Counters, error) {
	var r Counters
	err := c.do(ctx, http.MethodGet, "/v1/counters", nil, &r, 10*time.Second)
	return r, err
}

func (c *Client) Ack(ctx context.Context, epoch string, seq int64) error {
	return c.do(ctx, http.MethodPost, "/v1/counters/ack", AckRequest{Epoch: epoch, Seq: seq}, nil, 10*time.Second)
}

func (c *Client) Health(ctx context.Context) (Health, error) {
	var r Health
	err := c.do(ctx, http.MethodGet, "/v1/health", nil, &r, 5*time.Second)
	return r, err
}

func (c *Client) Logs(ctx context.Context, since time.Time) ([]LogLine, error) {
	var r []LogLine
	err := c.do(ctx, http.MethodGet, "/v1/logs?since="+url.QueryEscape(since.Format(time.RFC3339Nano)), nil, &r, 5*time.Second)
	return r, err
}
