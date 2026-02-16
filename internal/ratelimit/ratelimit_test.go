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

	// Manually age the stale bucket beyond expiry
	expiry := l.bucketExpiry()
	l.mu.Lock()
	l.buckets["stale"].lastCheck = time.Now().Add(-expiry - time.Second)
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

func TestEvictRespectsRefillTime(t *testing.T) {
	// Low rate: 1/min, burst 10 → full refill takes 10 min
	l := New(1, 10)
	defer l.Stop()

	expiry := l.bucketExpiry()
	expectedMin := 10 * time.Minute
	if expiry < expectedMin {
		t.Fatalf("expiry should be >= %v for slow rate, got %v", expectedMin, expiry)
	}

	// Drain all tokens
	l.Allow("user")
	for i := 0; i < 10; i++ {
		l.Allow("user")
	}

	// Age bucket to 5 minutes — should NOT be evicted (refill incomplete)
	l.mu.Lock()
	l.buckets["user"].lastCheck = time.Now().Add(-5 * time.Minute)
	l.mu.Unlock()

	l.evictStale()

	l.mu.Lock()
	_, exists := l.buckets["user"]
	l.mu.Unlock()
	if !exists {
		t.Fatal("bucket should not be evicted before full refill time")
	}
}

func TestStopMultipleCalls(t *testing.T) {
	l := New(60, 2)
	// Should not panic
	l.Stop()
	l.Stop()
	l.Stop()
}

func TestBucketExpiryFloor(t *testing.T) {
	// Very fast rate: 6000/min, burst 1 → refill ~0.01s, should floor to 1 min
	l := New(6000, 1)
	defer l.Stop()

	expiry := l.bucketExpiry()
	if expiry < time.Minute {
		t.Fatalf("expiry should be >= 1 minute, got %v", expiry)
	}
}
