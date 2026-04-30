package main

import (
	"errors"
	"testing"

	"altpocket/internal/fetcher"
)

func TestClassifyFetchErrorNoContent(t *testing.T) {
	if got := classifyFetchError(fetcher.ErrNoContent); got != "no_content" {
		t.Fatalf("expected no_content, got %q", got)
	}
}

func TestClassifyFetchErrorUnknown(t *testing.T) {
	if got := classifyFetchError(errors.New("unknown")); got != "fetch_failed" {
		t.Fatalf("expected fetch_failed, got %q", got)
	}
}

// TestClassifyFetchErrorBlockedIP covers Requirement 3 AC-2: SSRF rejections
// must surface a distinct reason code so they can be aggregated separately
// from generic fetch failures (NFR 2.2).
func TestClassifyFetchErrorBlockedIP(t *testing.T) {
	t.Run("sentinel_error", func(t *testing.T) {
		if got := classifyFetchError(fetcher.ErrBlockedIP); got != "blocked_ip" {
			t.Fatalf("expected blocked_ip, got %q", got)
		}
	})
	t.Run("wrapped_blocked_ip_error", func(t *testing.T) {
		// A wrapped *BlockedIPError must still classify as blocked_ip so the
		// dial-time TOCTOU rejection (which produces *BlockedIPError) is
		// recognized.
		wrapped := &wrappedErr{inner: &blockedSentinel{}}
		if got := classifyFetchError(wrapped); got != "blocked_ip" {
			t.Fatalf("expected blocked_ip via errors.Is, got %q", got)
		}
	})
}

// blockedSentinel is a minimal stand-in that satisfies errors.Is(_, ErrBlockedIP)
// without depending on the internal type. We use it via the Unwrap chain.
type blockedSentinel struct{}

func (b *blockedSentinel) Error() string { return "blocked" }
func (b *blockedSentinel) Unwrap() error { return fetcher.ErrBlockedIP }

type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }
