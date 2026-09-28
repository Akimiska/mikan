package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"mikan/internal/panel/store/db"
)

const (
	KeyAdminPath  = "admin_path"
	KeySubPath    = "sub_path"
	KeyPublicHost = "public_host"
	KeyPanelPort  = "panel_port"
	KeyDomain     = "domain"
	KeyACMEEmail  = "acme_email"
	KeyGroupMain  = "sub_group_main" // subscription group names, see subs.Groups
	KeyGroupAuto  = "sub_group_auto"
	KeyRouting    = "sub_routing" // subs.Routing
)

type Settings struct{ q *db.Queries }

func New(q *db.Queries) *Settings { return &Settings{q: q} }

// Get decodes the JSON value stored under key. ok is false when the key is absent.
func Get[T any](ctx context.Context, s *Settings, key string) (v T, ok bool, err error) {
	raw, err := s.q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, false, fmt.Errorf("setting %s: %w", key, err)
	}
	return v, true, nil
}

func Set[T any](ctx context.Context, s *Settings, key string, v T) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: string(raw)})
}

func (s *Settings) String(ctx context.Context, key string) (string, error) {
	v, _, err := Get[string](ctx, s, key)
	return v, err
}

type Paths struct {
	Admin string
	Sub   string
}

func (s *Settings) Paths(ctx context.Context) (Paths, error) {
	a, err := s.String(ctx, KeyAdminPath)
	if err != nil {
		return Paths{}, err
	}
	sub, err := s.String(ctx, KeySubPath)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Admin: a, Sub: sub}, nil
}

// Endpoint is how clients reach the panel: host is an IP or a domain.
type Endpoint struct {
	Host string
	Port int
}

func (s *Settings) Endpoint(ctx context.Context) (Endpoint, error) {
	host, err := s.String(ctx, KeyDomain)
	if err != nil {
		return Endpoint{}, err
	}
	if host == "" {
		if host, err = s.String(ctx, KeyPublicHost); err != nil {
			return Endpoint{}, err
		}
	}
	port, _, err := Get[int](ctx, s, KeyPanelPort)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Host: host, Port: port}, nil
}
