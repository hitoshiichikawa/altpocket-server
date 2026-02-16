package ratelimit

import (
	"sync"
	"time"
)

const cleanupInterval = 5 * time.Minute
const bucketExpiry = 10 * time.Minute

type bucket struct {
	tokens    float64
	lastCheck time.Time
}

type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
	stop    chan struct{}
}

func New(ratePerMinute int, burst int) *Limiter {
	l := &Limiter{
		buckets: map[string]*bucket{},
		rate:    float64(ratePerMinute) / 60.0,
		burst:   float64(burst),
		stop:    make(chan struct{}),
	}
	go l.cleanup()
	return l
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

// Stop halts the background cleanup goroutine.
func (l *Limiter) Stop() {
	close(l.stop)
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.evictStale()
		case <-l.stop:
			return
		}
	}
}

func (l *Limiter) evictStale() {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, b := range l.buckets {
		if now.Sub(b.lastCheck) > bucketExpiry {
			delete(l.buckets, key)
		}
	}
}
