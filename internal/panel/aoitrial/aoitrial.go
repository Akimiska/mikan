// Package aoitrial is the AoiVPN fork's clean-up of expired trials: a free trial that ended
// and was not bought on is deleted, so the panel's list and the nodes keep only the people
// who use the service. The trial itself stays recorded (trials, user_id set to NULL): the
// account is not given another.
package aoitrial

import (
	"context"
	"log/slog"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store"
)

// Grace: an expired trial waits for the bot's "it ended" notice (which offers to buy), or
// this long when there is nobody to tell (the AoiVPN app's trial has no chat).
const Grace = time.Hour

type Deps struct {
	Store *store.Store
	Users *domain.Users
	// TrialTariff is the tariff the trial gives (billing's config); 0: no trial.
	TrialTariff func(ctx context.Context) int64
	Now         func() time.Time
	Log         *slog.Logger
}

type Service struct{ d Deps }

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{d: d}
}

// Run deletes the expired trials every ten minutes until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		if n, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			s.d.Log.Warn("trials: sweep", "err", err)
		} else if n > 0 {
			s.d.Log.Info("trials: expired trials deleted", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Sweep deletes the trials that ended: a subscription the bot's trial made and still on the
// trial tariff (not bought on), or one the trials record names (the legacy bot's trials,
// carried over as ordinary subscriptions); never paid for, its end told by the bot or Grace
// past.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	tariff := s.d.TrialTariff(ctx)
	if tariff == 0 {
		return 0, nil
	}
	now := s.d.Now().Unix()
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT u.id FROM users u
		WHERE (u.source = $1 AND u.tariff_id = $2 OR EXISTS (SELECT 1 FROM trials t WHERE t.user_id = u.id))
		  AND u.expires_at IS NOT NULL AND u.expires_at <= $3
		  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.user_id = u.id AND p.status IN ('paid', 'applied'))
		  AND (u.expires_at <= $4 OR EXISTS (SELECT 1 FROM tg_notices n
		       WHERE n.user_id = u.id AND n.kind = 'expired' AND n.period = u.expires_at))`,
		domain.UserFromTrial, tariff, now, now-int64(Grace/time.Second))
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if err := s.d.Users.Delete(ctx, id); err != nil {
			s.d.Log.Warn("trials: delete", "user", id, "err", err)
			continue
		}
		n++
	}
	return n, nil
}
