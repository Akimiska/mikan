package aoiapp

import (
	"sync"
	"time"
)

// limiter allows max requests per key (a client address) within window.
type limiter struct {
	max    int
	window time.Duration

	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	// Old keys go on the way, so the map stays as small as the recent clients.
	if len(l.hits) > 4096 {
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}
