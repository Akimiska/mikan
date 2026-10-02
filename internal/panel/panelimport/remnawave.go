package panelimport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Remnawave 3.x: GET /api/users/stream?size=&cursor= with an API token, every answer in
// {"response": …}. Traffic is in userTraffic, "never" is a term in the year 2099, and the
// backend drops a connection without the headers its reverse proxy adds.

const remnawavePage = 500

type remnawaveUser struct {
	ID                int64   `json:"id"`
	ShortUUID         string  `json:"shortUuid"`
	Username          string  `json:"username"`
	Status            string  `json:"status"`
	TrafficLimitBytes int64   `json:"trafficLimitBytes"`
	ExpireAt          string  `json:"expireAt"`
	Description       *string `json:"description"`
	TelegramID        *int64  `json:"telegramId"`
	Email             *string `json:"email"`
	HWIDDeviceLimit   *int64  `json:"hwidDeviceLimit"`
	UserTraffic       struct {
		UsedTrafficBytes         int64 `json:"usedTrafficBytes"`
		LifetimeUsedTrafficBytes int64 `json:"lifetimeUsedTrafficBytes"`
	} `json:"userTraffic"`
}

func fetchRemnawave(ctx context.Context, hc *http.Client, src Source) ([]User, error) {
	if src.Token == "" {
		return nil, ErrAuth
	}
	var out []User
	cursor := ""
	for {
		q := url.Values{"size": {strconv.Itoa(remnawavePage)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		req, err := http.NewRequest(http.MethodGet, src.URL+"/api/users/stream?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+src.Token)
		// A reverse proxy in front overwrites these; straight to the backend they are needed.
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		var page struct {
			Response struct {
				Users      []remnawaveUser `json:"users"`
				NextCursor *string         `json:"nextCursor"`
				HasMore    bool            `json:"hasMore"`
			} `json:"response"`
		}
		if err := getJSON(ctx, hc, req, &page); err != nil {
			return nil, err
		}
		for _, ru := range page.Response.Users {
			u, err := ru.user()
			if err != nil {
				return nil, err
			}
			out = append(out, u)
		}
		if len(out) > maxUsers {
			return nil, fmt.Errorf("%w: more than %d users", ErrAnswer, maxUsers)
		}
		if !page.Response.HasMore || page.Response.NextCursor == nil || *page.Response.NextCursor == "" || *page.Response.NextCursor == cursor {
			return out, nil
		}
		cursor = *page.Response.NextCursor
	}
}

func (r remnawaveUser) user() (User, error) {
	u := User{Name: strings.TrimSpace(r.Username), Token: strings.TrimSpace(r.ShortUUID),
		Used: r.UserTraffic.UsedTrafficBytes, Lifetime: r.UserTraffic.LifetimeUsedTrafficBytes}
	if u.Name == "" {
		return u, fmt.Errorf("%w: a user without a username", ErrAnswer)
	}
	switch r.Status {
	case "DISABLED":
		u.Status = StatusDisabled
	case "LIMITED":
		u.Status = StatusLimited
	case "EXPIRED":
		u.Status = StatusExpired
	default:
		u.Status = StatusActive
	}
	if r.TrafficLimitBytes > 0 {
		u.TrafficLimit = r.TrafficLimitBytes
	}
	if u.Lifetime < u.Used {
		u.Lifetime = u.Used
	}
	if r.ExpireAt != "" {
		t, err := time.Parse(time.RFC3339Nano, r.ExpireAt)
		if err != nil {
			return u, fmt.Errorf("%w: user %s: expireAt %q", ErrAnswer, u.Name, r.ExpireAt)
		}
		if t.Year() < 2099 { // Remnawave's "never"
			u.Expires = t.UTC()
		}
	}
	if r.Description != nil {
		u.Note = *r.Description
	}
	switch {
	case r.TelegramID != nil && *r.TelegramID != 0:
		u.Contact = "tg:" + strconv.FormatInt(*r.TelegramID, 10)
	case r.Email != nil:
		u.Contact = *r.Email
	}
	if r.HWIDDeviceLimit != nil && *r.HWIDDeviceLimit > 0 {
		u.DeviceLimit = *r.HWIDDeviceLimit
	}
	return u, nil
}
