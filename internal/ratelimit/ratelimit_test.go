package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterAllow(t *testing.T) {
	l := New(60, 2) // 1 token/sec, burst 2
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
