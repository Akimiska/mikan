package panelimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Report is what an import did.
type Report struct {
	Created int      `json:"created"`
	Skipped []string `json:"skipped" doc:"Имена, которые уже есть в mikan: эти пользователи не перенесены"`
	Links   int      `json:"links" doc:"Пользователи, чьи старые ссылки подписки будут работать"`
	Failed  []string `json:"failed" doc:"Имя и причина для тех, кого не удалось создать"`
}

// Preview is what an import would do, without doing it.
type Preview struct {
	Total    int            `json:"total"`
	New      int            `json:"new" doc:"Будут созданы"`
	Taken    []string       `json:"taken" doc:"Имена, которые уже есть в mikan"`
	Statuses map[string]int `json:"statuses"`
	OnHold   int            `json:"on_hold" doc:"Пользователи «на паузе»: в mikan их срок пойдёт с момента импорта"`
}

// Check counts what Apply would do.
func Check(ctx context.Context, q *db.Queries, list []User) (Preview, error) {
	p := Preview{Total: len(list), Taken: []string{}, Statuses: map[string]int{}}
	seen := map[string]bool{}
	for _, u := range list {
		p.Statuses[u.Status]++
		if u.Status == StatusOnHold {
			p.OnHold++
		}
		taken, err := q.UserNameTaken(ctx, u.Name)
		if err != nil {
			return p, err
		}
		if taken || seen[u.Name] {
			p.Taken = append(p.Taken, u.Name)
			continue
		}
		seen[u.Name] = true
		p.New++
	}
	return p, nil
}

// Apply makes the users on the plan tariffID, with the old panel's limit, term, traffic
// used and device limit, and files the keys their old links resolve to: Remnawave's
// short UUID, or for Marzban and PasarGuard the name and id their signed tokens carry
// (Verifier). A name mikan already has is skipped, never merged into someone else.
func Apply(ctx context.Context, st *store.Store, users *domain.Users, now time.Time, kind Kind, tariffID int64, list []User) (Report, error) {
	r := Report{Skipped: []string{}, Failed: []string{}}
	if _, err := st.Q.GetTariff(ctx, tariffID); errors.Is(err, sql.ErrNoRows) {
		return r, domain.ErrNotFound
	} else if err != nil {
		return r, err
	}
	for _, in := range list {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		taken, err := st.Q.UserNameTaken(ctx, in.Name)
		if err != nil {
			return r, err
		}
		if taken {
			r.Skipped = append(r.Skipped, in.Name)
			continue
		}
		linked, err := applyOne(ctx, st, users, now, kind, tariffID, in)
		if err != nil {
			r.Failed = append(r.Failed, in.Name+": "+err.Error())
			continue
		}
		r.Created++
		if linked {
			r.Links++
		}
	}
	return r, nil
}

func applyOne(ctx context.Context, st *store.Store, users *domain.Users, now time.Time, kind Kind, tariffID int64, in User) (linked bool, err error) {
	u, err := users.Create(ctx, domain.CreateInput{Name: in.Name, Contact: in.Contact, Note: in.Note, TariffID: tariffID})
	if err != nil {
		return false, err
	}
	p := domain.Patch{}
	if in.TrafficLimit > 0 {
		p.TrafficLimit = &in.TrafficLimit
	} else {
		p.ClearTrafficLimit = true
	}
	switch {
	case !in.Expires.IsZero():
		p.ExpiresAt = &in.Expires
	case in.Status == StatusOnHold && in.OnHold > 0:
		// mikan has no term that waits for the first connection: it starts now.
		exp := now.Add(in.OnHold)
		p.ExpiresAt = &exp
	default:
		p.ClearExpiry = true
	}
	if in.DeviceLimit > 0 {
		p.DeviceLimit = &in.DeviceLimit
	}
	if in.Status == StatusDisabled {
		off := true
		p.Disabled = &off
	}
	if _, err := users.Update(ctx, u.ID, p); err != nil {
		return false, fmt.Errorf("limits: %w", err)
	}
	if in.Used > 0 || in.Lifetime > 0 {
		if err := st.Q.SetImportedUsage(ctx, db.SetImportedUsageParams{ID: u.ID, UsedDown: in.Used, TotalDown: in.Lifetime, UpdatedAt: now.Unix()}); err != nil {
			return false, fmt.Errorf("traffic: %w", err)
		}
	}
	var keys []string
	switch kind {
	case Remnawave:
		if in.Token != "" {
			keys = append(keys, in.Token)
		}
	case Marzban:
		keys = append(keys, "name:"+in.Name)
	case PasarGuard:
		keys = append(keys, "name:"+in.Name)
		if in.SourceID > 0 {
			keys = append(keys, "id:"+strconv.FormatInt(in.SourceID, 10))
		}
	}
	for _, k := range keys {
		if err := st.Q.AddLegacySubToken(ctx, db.AddLegacySubTokenParams{Token: k, UserID: u.ID, Source: string(kind)}); err != nil {
			return false, fmt.Errorf("old link: %w", err)
		}
	}
	return len(keys) > 0, nil
}
