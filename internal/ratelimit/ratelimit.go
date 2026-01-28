package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens    float64
	lastCheck time.Time
}

type Limiter struct {
	mu     sync.Mutex
	buckets map[string]*bucket
	rate   float64
	burst  float64
}

func New(ratePerMinute int, burst int) *Limiter {
	return &Limiter{
		buckets: map[string]*bucket{},
		rate:   float64(ratePerMinute) / 60.0,
		burst:  float64(burst),
	}
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, lastCheck: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastCheck = now
	if b.tokens < 1 {
		return false
	}
	b.tokens -= 1
	return true
}
