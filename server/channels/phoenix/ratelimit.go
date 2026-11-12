// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"sync"
	"time"
)

// Limiter is a fixed-window per-key limiter (contracts C3: 120/min caller, 30/min IP or session).
type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	start time.Time
	count int
}

func NewLimiter(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: limit, window: window, now: now, buckets: map[string]*bucket{}}
}

// Allow records one hit; when the key is over its limit it returns the seconds to wait.
func (l *Limiter) Allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.swept) > l.window {
		for k, b := range l.buckets {
			if now.Sub(b.start) >= l.window {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}
	b := l.buckets[key]
	if b == nil || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	if b.count >= l.limit {
		wait := int(b.start.Add(l.window).Sub(now).Seconds()) + 1
		if wait < 1 {
			wait = 1
		}
		return false, wait
	}
	b.count++
	return true, 0
}
