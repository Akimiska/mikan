package subs

import (
	"sync"
	"time"
)

const (
	promoAttemptLimit  = 5
	promoAttemptWindow = 10 * time.Minute
	promoBlockDuration = 15 * time.Minute
)

type promoAttempt struct {
	attempts int
	window   time.Time
	blocked  time.Time
}

// promoLimiter caps code checks per Telegram account. Successful checks also consume a
// slot, so a known code cannot reset the limit. Old entries are pruned as requests arrive.
type promoLimiter struct {
	mu       sync.Mutex
	accounts map[int64]promoAttempt
}

func (l *promoLimiter) allow(tgID int64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.accounts == nil {
		l.accounts = make(map[int64]promoAttempt)
	}
	l.prune(now)
	a := l.accounts[tgID]
	if now.Before(a.blocked) {
		return false
	}
	if a.window.IsZero() || !now.Before(a.window.Add(promoAttemptWindow)) {
		a = promoAttempt{window: now}
	}
	a.attempts++
	if a.attempts >= promoAttemptLimit {
		a.blocked = now.Add(promoBlockDuration)
	}
	l.accounts[tgID] = a
	return true
}

func (l *promoLimiter) prune(now time.Time) {
	for id, a := range l.accounts {
		if !now.Before(a.blocked) && (a.window.IsZero() || !now.Before(a.window.Add(promoAttemptWindow))) {
			delete(l.accounts, id)
		}
	}
}
