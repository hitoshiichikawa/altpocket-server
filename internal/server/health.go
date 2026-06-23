package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"log/slog"
)

// readyDBPingTimeout is the deadline applied to the DB ping issued by
// /readyz. The value is chosen to keep readiness probes responsive
// (NFR 1.2) while still allowing one TCP round-trip + a small DB
// dispatch margin. (Requirement 2.5)
const readyDBPingTimeout = 2 * time.Second

// pinger is the minimal contract /readyz needs from the persistence
// layer. Keeping it as a small local interface lets the handler depend
// on behavior (one call: ping with a deadline) rather than the concrete
// *pgxpool.Pool, which makes the success / failure / timeout branches
// covered by Requirement 2 testable without spinning up a real
// PostgreSQL instance. The production implementation is *pgxpool.Pool
// itself (it already exposes Ping(ctx) error).
type pinger interface {
	Ping(ctx context.Context) error
}

// handleHealth is the liveness endpoint. It MUST NOT touch the
// database: the contract documented in Requirement 1 is that /healthz
// returns HTTP 200 + body "ok" whenever the process is able to serve
// HTTP, regardless of DB state. Existing load balancer / smoke test
// callers depend on this exact response shape (Requirement 1.4,
// NFR 3.1, NFR 3.2).
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReady is the readiness endpoint. It issues a single DB ping
// bounded by readyDBPingTimeout (Requirement 2.1, 2.5) and maps the
// outcome to:
//   - HTTP 200 + body "ok"           on success (Requirement 2.2)
//   - HTTP 503 + body "unavailable"  on failure or timeout
//     (Requirement 2.3, 2.4)
//
// On 503 a structured WARN log entry is emitted so operators can
// observe failure spikes (Requirement 2.6). The response body and log
// fields are intentionally short and opaque: no DB connection string,
// driver-level stack trace, SQL text, or host metadata is included
// (Requirement 2.7, NFR 2.1, NFR 2.2).
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	p := s.dbPinger()
	if p == nil {
		// No DB wired in. Treat as unavailable rather than crashing so
		// the load balancer can still strip this instance from the
		// rotation (Requirement 2.3).
		s.logReadyFailure(r.Context(), "pinger_unavailable")
		writeReadyUnavailable(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readyDBPingTimeout)
	defer cancel()

	if err := p.Ping(ctx); err != nil {
		reason := "db_ping_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "db_ping_timeout"
		}
		s.logReadyFailure(r.Context(), reason)
		writeReadyUnavailable(w)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// dbPinger returns the pinger backing /readyz. Tests inject a fake
// via Server.readyPingerFn; production code falls back to the
// underlying *pgxpool.Pool exposed by the Store (which already has
// Ping(ctx) error).
func (s *Server) dbPinger() pinger {
	if s.readyPingerFn != nil {
		return pingerFunc(s.readyPingerFn)
	}
	if s.store == nil || s.store.DB == nil {
		return nil
	}
	return s.store.DB
}

// pingerFunc adapts a plain function into the pinger interface so
// tests can express the ping behavior inline without declaring a
// dedicated struct.
type pingerFunc func(ctx context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func writeReadyUnavailable(w http.ResponseWriter) {
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("unavailable"))
}

// logReadyFailure emits a structured WARN entry for /readyz failures.
// It deliberately omits the driver-supplied error string: pgx (and
// other database drivers) can embed DSN fragments, host names, or SQL
// text in error messages, and NFR 2.1 forbids leaking those to logs.
// A short categorical reason code plus the request ID is enough for
// operators to correlate spikes with upstream incidents without
// risking secret exposure (Requirement 3.3, NFR 2.1).
func (s *Server) logReadyFailure(ctx context.Context, reason string) {
	if s.logger == nil {
		return
	}
	s.logger.Warn("health.ready.unavailable",
		slog.String("request_id", s.requestID(ctx)),
		slog.String("reason", reason),
	)
}
