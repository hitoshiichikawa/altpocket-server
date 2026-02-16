package ratelimit

import (
	"math"
	"sync"
	"time"
)

const cleanupInterval = 5 * time.Minute

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
	once    sync.Once
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

// Stop halts the background cleanup goroutine. Safe to call multiple times.
func (l *Limiter) Stop() {
	l.once.Do(func() { close(l.stop) })
}

// bucketExpiry returns the duration after which an idle bucket can be evicted.
// A bucket is only stale when enough time has passed for it to fully refill,
// so evicting it is equivalent to the natural state — no rate-limit bypass.
func (l *Limiter) bucketExpiry() time.Duration {
	// Time for a full refill: burst / rate seconds.
	// rate is tokens-per-second; if zero, fall back to a safe default.
	if l.rate <= 0 {
		return 10 * time.Minute
	}
	refillSec := math.Ceil(l.burst / l.rate)
	expiry := time.Duration(refillSec) * time.Second
	// Floor at 1 minute to avoid churn for very fast rates.
	if expiry < time.Minute {
		expiry = time.Minute
	}
	return expiry
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
	expiry := l.bucketExpiry()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, b := range l.buckets {
		if now.Sub(b.lastCheck) > expiry {
			delete(l.buckets, key)
		}
	}
}
