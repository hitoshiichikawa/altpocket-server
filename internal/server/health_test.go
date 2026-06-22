package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"altpocket/internal/config"
	"altpocket/internal/ratelimit"
)

// TestHandleHealthAlwaysReturnsOK guards the liveness contract:
// /healthz must answer HTTP 200 + body "ok" regardless of whether the
// DB is reachable, because existing load balancers, smoke tests, and
// the production deploy docs depend on that exact response shape
// (Requirement 1.1, 1.2, 1.3, 1.4).
//
// To prove that no DB ping happens during /healthz, we install a
// pinger that records each call and assert it stays at zero after
// the handler runs.
func TestHandleHealthAlwaysReturnsOK(t *testing.T) {
	cases := []struct {
		name      string
		pingerFn  func(ctx context.Context) error
		expectErr bool
	}{
		{
			name:     "pinger nil (no DB wired)",
			pingerFn: nil,
		},
		{
			name: "pinger would fail if invoked",
			pingerFn: func(ctx context.Context) error {
				return errors.New("connection refused")
			},
			expectErr: true,
		},
		{
			name: "pinger would succeed",
			pingerFn: func(ctx context.Context) error {
				return nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthTestServer()
			var pingCalls atomic.Int32
			if tc.pingerFn != nil {
				s.readyPingerFn = func(ctx context.Context) error {
					pingCalls.Add(1)
					return tc.pingerFn(ctx)
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			rr := httptest.NewRecorder()

			s.handleHealth(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
			if rr.Body.String() != "ok" {
				t.Fatalf("expected body \"ok\", got %q", rr.Body.String())
			}
			if got := pingCalls.Load(); got != 0 {
				t.Fatalf("/healthz must not invoke DB ping, got %d calls", got)
			}
		})
	}
}

// TestHandleReadySuccess covers Requirement 2.1 + 2.2: /readyz issues
// exactly one DB ping per request and, on success, answers HTTP 200
// + body "ok".
func TestHandleReadySuccess(t *testing.T) {
	s := newAuthTestServer()
	var pingCalls atomic.Int32
	s.readyPingerFn = func(ctx context.Context) error {
		pingCalls.Add(1)
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("expected body \"ok\", got %q", rr.Body.String())
	}
	if got := pingCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 DB ping, got %d", got)
	}
}

// TestHandleReadyFailureReturns503 covers Requirement 2.3:
// any DB ping error (connection refused, auth rejected, DB-side
// error) must collapse to HTTP 503 + body "unavailable".
func TestHandleReadyFailureReturns503(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "connection refused", err: errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")},
		{name: "auth rejected", err: errors.New("pq: password authentication failed")},
		{name: "generic db error", err: errors.New("server closed the connection unexpectedly")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthTestServer()
			s.readyPingerFn = func(ctx context.Context) error {
				return tc.err
			}

			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rr := httptest.NewRecorder()

			s.handleReady(rr, req)

			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected 503, got %d (body=%q)", rr.Code, rr.Body.String())
			}
			if rr.Body.String() != "unavailable" {
				t.Fatalf("expected body \"unavailable\", got %q", rr.Body.String())
			}
		})
	}
}

// TestHandleReadyTimeoutReturns503 covers Requirement 2.4 + 2.5: if the
// DB ping does not return within the 2-second budget, /readyz must
// answer HTTP 503 + body "unavailable" without blocking the request
// goroutine indefinitely.
//
// We simulate a slow DB by having the fake pinger block on the
// context's Done channel. The handler-installed deadline must fire
// well before our 5-second test guard.
func TestHandleReadyTimeoutReturns503(t *testing.T) {
	s := newAuthTestServer()
	s.readyPingerFn = func(ctx context.Context) error {
		// Block until the handler's WithTimeout cancels us.
		<-ctx.Done()
		return ctx.Err()
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	start := time.Now()
	go func() {
		s.handleReady(rr, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("handleReady did not return within 5s; timeout enforcement is broken")
	}

	elapsed := time.Since(start)
	// The handler's deadline is 2s. We allow a comfortable upper
	// bound (4s) to absorb CI scheduling jitter without making the
	// test flaky, while still rejecting the "no timeout enforced"
	// regression where the goroutine would block for the full 5s.
	if elapsed > 4*time.Second {
		t.Fatalf("handleReady took %v, expected < 4s under the 2s ping timeout", elapsed)
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on timeout, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "unavailable" {
		t.Fatalf("expected body \"unavailable\", got %q", rr.Body.String())
	}
}

// TestHandleReadyPingerUnavailableReturns503 covers the edge case
// where no DB pinger is wired in (e.g. Server constructed without a
// Store). The handler must still answer 503 rather than panicking, so
// upstream load balancers can strip the instance from rotation
// (Requirement 2.3, Requirement 4.4).
func TestHandleReadyPingerUnavailableReturns503(t *testing.T) {
	s := newAuthTestServer() // store == nil, readyPingerFn == nil

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "unavailable" {
		t.Fatalf("expected body \"unavailable\", got %q", rr.Body.String())
	}
}

// TestHandleReadyResponseBodyContainsNoSecrets defends Requirement
// 2.7 / NFR 2.2: when the DB ping fails, the 503 response body must
// stay opaque. It must not echo the driver-supplied error message,
// because pgx error strings can contain DSN fragments / SQL text /
// internal stack traces in some failure modes.
func TestHandleReadyResponseBodyContainsNoSecrets(t *testing.T) {
	sensitive := "postgres://alice:supersecretpw@db.internal:5432/altpocket sslmode=disable"
	s := newAuthTestServer()
	s.readyPingerFn = func(ctx context.Context) error {
		return errors.New(sensitive)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	s.handleReady(rr, req)

	body := rr.Body.String()
	if body != "unavailable" {
		t.Fatalf("expected body \"unavailable\", got %q", body)
	}
	// Belt-and-suspenders: even if the body shape changes in the
	// future, none of the sensitive fragments must ever leak.
	for _, frag := range []string{"supersecretpw", "postgres://", "db.internal", "alice"} {
		if containsSubstring(body, frag) {
			t.Fatalf("response body leaked sensitive fragment %q: %s", frag, body)
		}
	}
}

// containsSubstring is a small wrapper so the assertion above reads
// naturally without pulling in strings just for this check.
func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestHandleReadyLogContainsNoSecrets extends Requirement 2.7 / NFR 2.1
// coverage to the *log* path: when the DB ping fails with an error
// whose message contains a DSN-shaped secret (the exact failure mode
// pgx can produce when the underlying connect attempt embeds host /
// user / password fragments in its error string), the structured WARN
// entry emitted for /readyz must not echo any of those fragments.
//
// The existing TestHandleReadyResponseBodyContainsNoSecrets only
// guards the response body. Without this test, a regression that re-
// adds slog.String("error", err.Error()) would silently leak secrets
// into operator logs while still passing the body assertion.
func TestHandleReadyLogContainsNoSecrets(t *testing.T) {
	sensitive := "postgres://alice:supersecretpw@db.internal:5432/altpocket sslmode=disable"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cfg := config.Config{}
	s := New(cfg, nil, ratelimit.New(60, 60), logger, nil)
	s.readyPingerFn = func(ctx context.Context) error {
		return errors.New(sensitive)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	s.handleReady(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}

	logged := buf.String()
	if logged == "" {
		t.Fatalf("expected a WARN log entry on /readyz failure, got none")
	}
	for _, frag := range []string{"supersecretpw", "postgres://", "db.internal", "alice"} {
		if containsSubstring(logged, frag) {
			t.Fatalf("structured log leaked sensitive fragment %q: %s", frag, logged)
		}
	}
}

// TestHandleReadyLogReasonDistinguishesTimeout asserts that when the
// DB ping fails specifically because the 2-second deadline fired, the
// structured log entry uses the categorical reason "db_ping_timeout"
// instead of the generic "db_ping_failed". Operators correlating
// /readyz failure spikes need to tell timeouts apart from connect
// refusals without having to inspect the (now redacted) underlying
// error string.
func TestHandleReadyLogReasonDistinguishesTimeout(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cfg := config.Config{}
	s := New(cfg, nil, ratelimit.New(60, 60), logger, nil)
	s.readyPingerFn = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		s.handleReady(rr, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("handleReady did not return within 5s")
	}

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on timeout, got %d", rr.Code)
	}
	logged := buf.String()
	if !containsSubstring(logged, "db_ping_timeout") {
		t.Fatalf("expected reason=db_ping_timeout in log, got: %s", logged)
	}
}

