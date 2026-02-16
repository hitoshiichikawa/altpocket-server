package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterAllow(t *testing.T) {
	l := New(60, 2) // 1 token/sec, burst 2
	defer l.Stop()
	key := "user-1"

	if !l.Allow(key) {
		t.Fatalf("first token should be allowed")
	}
	if !l.Allow(key) {
		t.Fatalf("second token (burst) should be allowed")
	}
	if l.Allow(key) {
		t.Fatalf("third token should be rate-limited")
	}

	time.Sleep(1100 * time.Millisecond)
	if !l.Allow(key) {
		t.Fatalf("token should refill after 1s")
	}
}

func TestEvictStale(t *testing.T) {
	l := New(60, 2)
	defer l.Stop()

	l.Allow("active")
	l.Allow("stale")

	// Manually age the stale bucket
	l.mu.Lock()
	l.buckets["stale"].lastCheck = time.Now().Add(-bucketExpiry - time.Second)
	l.mu.Unlock()

	l.evictStale()

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.buckets["stale"]; ok {
		t.Fatal("stale bucket should have been evicted")
	}
	if _, ok := l.buckets["active"]; !ok {
		t.Fatal("active bucket should still exist")
	}
}
