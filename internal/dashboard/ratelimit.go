package dashboard

import (
	"sync"
	"time"
)

type rateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	clients map[string]rateLimitEntry
}

type rateLimitEntry struct {
	start time.Time
	count int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &rateLimiter{
		window:  window,
		limit:   limit,
		clients: make(map[string]rateLimitEntry),
	}
}

func (l *rateLimiter) Allow(now time.Time, key string) bool {
	if l == nil || key == "" {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanup(now)
	entry, ok := l.clients[key]
	if !ok || now.Sub(entry.start) >= l.window {
		l.clients[key] = rateLimitEntry{
			start: now,
			count: 1,
		}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.clients[key] = entry
	return true
}

func (l *rateLimiter) cleanup(now time.Time) {
	for key, entry := range l.clients {
		if now.Sub(entry.start) >= l.window {
			delete(l.clients, key)
		}
	}
}
