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

// TestRunStartupCycleInvokesAllOnce verifies AC 1.1 / 1.2 / 1.3:
// the startup cycle must invoke the three step functions exactly once each.
func TestRunStartupCycleInvokesAllOnce(t *testing.T) {
	// Arrange
	var sessionCalls, refreshCalls, fetchCalls int
	sessionFn := func() { sessionCalls++ }
	refreshFn := func() { refreshCalls++ }
	fetchFn := func() { fetchCalls++ }

	// Act
	runStartupCycle(sessionFn, refreshFn, fetchFn)

	// Assert
	if sessionCalls != 1 {
		t.Fatalf("session cleanup: expected 1 call, got %d", sessionCalls)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh token cleanup: expected 1 call, got %d", refreshCalls)
	}
	if fetchCalls != 1 {
		t.Fatalf("fetch run: expected 1 call, got %d", fetchCalls)
	}
}

// TestRunStartupCycleOrderMatchesTickerLoop verifies AC 1.5:
// the order must be cleanupSessions -> cleanupRefreshTokens -> runOnce,
// which is the same order as the existing ticker loop body.
func TestRunStartupCycleOrderMatchesTickerLoop(t *testing.T) {
	// Arrange
	var order []string
	sessionFn := func() { order = append(order, "session") }
	refreshFn := func() { order = append(order, "refresh") }
	fetchFn := func() { order = append(order, "fetch") }

	// Act
	runStartupCycle(sessionFn, refreshFn, fetchFn)

	// Assert
	want := []string{"session", "refresh", "fetch"}
	if len(order) != len(want) {
		t.Fatalf("expected %d calls in order, got %d (%v)", len(want), len(order), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order mismatch at index %d: want %q, got %q (full=%v)", i, want[i], order[i], order)
		}
	}
}

// TestRunStartupCycleContinuesAfterStepReturn verifies AC 4.1 / 4.2 / 4.3:
// each step must run regardless of what the previous step did internally.
// The three step functions own their own error handling (logging + early
// return) and never panic; runStartupCycle must not short-circuit between
// them. We simulate "step internally handled an error and returned" by
// having each fn simply return; we then assert all three still ran.
func TestRunStartupCycleContinuesAfterStepReturn(t *testing.T) {
	// Arrange
	var calls []string
	// Each fn represents a step that already absorbed an error internally
	// (matches the real cleanupSessions/cleanupRefreshTokens/runOnce, which
	// log-and-return on failure rather than propagating).
	sessionFn := func() { calls = append(calls, "session") }
	refreshFn := func() { calls = append(calls, "refresh") }
	fetchFn := func() { calls = append(calls, "fetch") }

	// Act
	runStartupCycle(sessionFn, refreshFn, fetchFn)

	// Assert: all three executed; none was skipped.
	if len(calls) != 3 {
		t.Fatalf("expected all 3 steps to run, got %d (%v)", len(calls), calls)
	}
}
