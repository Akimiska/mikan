// Package httpjson is the one way the panel reads an answer of a service over HTTP: the body
// is read up to a limit, so an answer that does not end is refused instead of held in memory,
// and a status that is not 2xx is one error, *StatusError, that keeps the start of the body
// and the headers for a service that explains itself there.
package httpjson

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ErrTooLarge: the answer is longer than the limit its reader set.
var ErrTooLarge = errors.New("httpjson: the answer is too large")

// StatusError is an answer whose status is not 2xx.
type StatusError struct {
	Code   int
	Header http.Header
	// Body is the start of the answer (bodyKept bytes at most): the error a service gives in it.
	Body []byte
}

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// bodyKept is how much of a refusal's body StatusError keeps.
const bodyKept = 4 << 10

// Read is what r gives, at most limit bytes: a longer answer is ErrTooLarge.
func Read(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

// Decode reads r's JSON into out, at most limit bytes of it.
func Decode(r io.Reader, limit int64, out any) error {
	data, err := Read(r, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// Fetch sends req with c and is the body of a 2xx answer, at most limit bytes of it; any
// other status is a *StatusError.
func Fetch(c *http.Client, req *http.Request, limit int64) ([]byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := status(resp); err != nil {
		return nil, err
	}
	return Read(resp.Body, limit)
}

// Do sends req with c and reads a 2xx answer's JSON into out (nil: the body is not read), at
// most limit bytes of it; any other status is a *StatusError.
func Do(c *http.Client, req *http.Request, limit int64, out any) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := status(resp); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return Decode(resp.Body, limit, out)
}

func status(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, bodyKept))
	return &StatusError{Code: resp.StatusCode, Header: resp.Header, Body: body}
}

// Get is Do of a GET of url that asks for JSON.
func Get(ctx context.Context, c *http.Client, url string, limit int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	return Do(c, req, limit, out)
}

// Code is the status of err when it is a *StatusError, 0 otherwise.
func Code(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// Unreachable: err is the request's own failure (no answer came: a dial, TLS, a timeout),
// not a status or an answer that could not be read.
func Unreachable(err error) bool {
	var ue *url.Error
	return errors.As(err, &ue)
}
