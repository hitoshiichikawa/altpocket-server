//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altpocket/internal/auth"
	"altpocket/internal/config"
	"altpocket/internal/ratelimit"
	"altpocket/internal/store"

	"github.com/go-chi/chi/v5"
)

// These tests exercise the PATCH /v1/items/{id}/status handler against
// a real PostgreSQL database. They are gated by `-tags=integration`
// and the TEST_DATABASE_URL env var (same convention as
// items_active_filters_integration_test.go and the store_item_status_test
// added by task 3).
//
// 4 cases live here (task 4 in tasks.md):
//   - TestHandleSetItemStatusSuccessReturns200            (Req 1.4 / 2.3-2.6)
//   - TestHandleSetItemStatusLogsTransitionFields         (NFR 3.1)
//   - TestHandleSetItemStatusNotFoundForMissingID         (NFR 2.1, ErrNoRows collapse)
//   - TestHandleSetItemStatusOtherUserItemReturns404      (NFR 2.1, ownership protection)
//
// The build tag here matches store_item_status_test.go so a single
// `go test -tags=integration ./internal/...` invocation runs the full
// store + handler integration suite.

// newStatusIntegrationServer builds a Server backed by a real *store.Store
// and a slog handler that writes JSON to the returned buffer. The buffer
// is used by TestHandleSetItemStatusLogsTransitionFields to assert the
// NFR 3.1 transition log fields without coupling to format internals.
func newStatusIntegrationServer(t *testing.T) (*Server, *bytes.Buffer, func()) {
	t.Helper()
	st, cleanup := newIntegrationStore(t)
	buf := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Config{
		PublicBaseURL: "https://www.example.invalid",
	}
	s := New(cfg, st, ratelimit.New(60, 60), logger, nil)
	return s, buf, cleanup
}

// newStatusPatchRequest constructs a PATCH request that mirrors the
// runtime path: chi's URL parameter context is populated explicitly so
// chi.URLParam(r, "id") returns the desired item id (the chi router
// would set this on a routed call, but direct handler invocation needs
// it injected).
func newStatusPatchRequest(t *testing.T, itemID, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch,
		"/v1/items/"+itemID+"/status",
		strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", itemID)
	req = req.WithContext(context.WithValue(req.Context(),
		chi.RouteCtxKey, rctx))
	return req
}

// seedStatusItem creates a fresh user and an item for them, returning
// (userID, itemID). The cleanup registered by seedItemsActiveFilterUser
// cascades to remove the item.
func seedStatusItem(t *testing.T, s *store.Store, ctx context.Context, hash string) (string, string) {
	t.Helper()
	userID := seedItemsActiveFilterUser(t, s, ctx)
	rawURL := "https://example.invalid/status-" + hash
	itemID, _, err := s.CreateItem(ctx, userID, rawURL, rawURL,
		"hash-"+hash, nil, "seed-"+hash, "")
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	return userID, itemID
}

// TestHandleSetItemStatusSuccessReturns200 pins the success contract
// (Req 1.4 / 2.3-2.6 from the server's perspective): valid status
// returns 200 with {"status":"<next>","item_id":"<id>"} body and the
// row in PostgreSQL is updated to the new value.
func TestHandleSetItemStatusSuccessReturns200(t *testing.T) {
	srv, _, cleanup := newStatusIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	userID, itemID := seedStatusItem(t, srv.store, ctx, "success")

	req := newStatusPatchRequest(t, itemID, `{"status":"read"}`)
	req = req.WithContext(auth.ContextWithUser(req.Context(),
		auth.User{ID: userID}))
	rr := httptest.NewRecorder()

	srv.handleSetItemStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}

	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rr.Body.String())
	}
	if body["status"] != "read" {
		t.Errorf("response status = %q, want %q", body["status"], "read")
	}
	if body["item_id"] != itemID {
		t.Errorf("response item_id = %q, want %q", body["item_id"], itemID)
	}

	// And the row in the database really did move.
	var got string
	if err := srv.store.DB.QueryRow(ctx,
		`SELECT status FROM items WHERE id = $1 AND user_id = $2`,
		itemID, userID).Scan(&got); err != nil {
		t.Fatalf("post-patch SELECT failed: %v", err)
	}
	if got != "read" {
		t.Errorf("items.status = %q, want %q (response said OK but DB did not move)", got, "read")
	}
}

// TestHandleSetItemStatusLogsTransitionFields exercises NFR 3.1: the
// transition log line must include user_id, item_id, prev, next and
// must NOT include the session cookie, Authorization header or refresh
// token. We send a fake Authorization header and a session-style cookie
// on the request to make the leak-check meaningful, then walk the
// captured slog JSON lines.
func TestHandleSetItemStatusLogsTransitionFields(t *testing.T) {
	srv, buf, cleanup := newStatusIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	userID, itemID := seedStatusItem(t, srv.store, ctx, "logs")
	// Drive a multi-step transition so prev / next are distinguishable.
	if _, err := srv.store.UpdateItemStatus(ctx, userID, itemID, store.ItemStatusRead); err != nil {
		t.Fatalf("preconditions: UpdateItemStatus -> read: %v", err)
	}
	buf.Reset()

	req := newStatusPatchRequest(t, itemID, `{"status":"archived"}`)
	// Sensitive headers / cookies the logger MUST NOT echo back. These
	// values are nonsense but distinctive enough to grep for in the
	// captured log buffer.
	req.Header.Set("Authorization", "Bearer SECRET-JWT-VALUE-DO-NOT-LOG")
	req.AddCookie(&http.Cookie{Name: "altpocket_session", Value: "SECRET-SESSION-COOKIE-DO-NOT-LOG"})
	req = req.WithContext(auth.ContextWithUser(req.Context(),
		auth.User{ID: userID}))
	rr := httptest.NewRecorder()

	srv.handleSetItemStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}

	// Parse each line in the buffer (slog JSON handler emits 1 line per
	// record). Find the one with msg == "items.status.update".
	var found map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not JSON: %v (line=%q)", err, line)
		}
		if rec["msg"] == "items.status.update" {
			found = rec
			break
		}
	}
	if found == nil {
		t.Fatalf("did not find items.status.update log line. captured:\n%s", buf.String())
	}

	if got, _ := found["user_id"].(string); got != userID {
		t.Errorf("log user_id = %q, want %q", got, userID)
	}
	if got, _ := found["item_id"].(string); got != itemID {
		t.Errorf("log item_id = %q, want %q", got, itemID)
	}
	if got, _ := found["prev"].(string); got != "read" {
		t.Errorf("log prev = %q, want %q (prev should reflect the value BEFORE this PATCH)", got, "read")
	}
	if got, _ := found["next"].(string); got != "archived" {
		t.Errorf("log next = %q, want %q", got, "archived")
	}

	// NFR 3.1 negative assertion: no sensitive header / cookie value
	// must appear anywhere in the captured log buffer (across all
	// emitted records, not just this one — the handler should not log
	// them at any point in the request lifecycle).
	captured := buf.String()
	for _, sensitive := range []string{
		"SECRET-JWT-VALUE-DO-NOT-LOG",
		"SECRET-SESSION-COOKIE-DO-NOT-LOG",
	} {
		if strings.Contains(captured, sensitive) {
			t.Errorf("structured log leaked sensitive value %q. full buffer:\n%s",
				sensitive, captured)
		}
	}
}

// TestHandleSetItemStatusNotFoundForMissingID covers the
// pgx.ErrNoRows-from-store collapse: a syntactically valid UUID that
// does not correspond to any row returns 404 not_found rather than
// 500 db_error.
func TestHandleSetItemStatusNotFoundForMissingID(t *testing.T) {
	srv, _, cleanup := newStatusIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	userID := seedItemsActiveFilterUser(t, srv.store, ctx)
	// A well-formed UUID that does not correspond to any row.
	missingID := "00000000-0000-0000-0000-000000000000"

	req := newStatusPatchRequest(t, missingID, `{"status":"read"}`)
	req = req.WithContext(auth.ContextWithUser(req.Context(),
		auth.User{ID: userID}))
	rr := httptest.NewRecorder()

	srv.handleSetItemStatus(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not_found") {
		t.Fatalf("expected not_found body, got %q", rr.Body.String())
	}
}

// TestHandleSetItemStatusOtherUserItemReturns404 pins the NFR 2.1
// ownership contract: user B cannot mutate user A's item. The handler
// returns 404 (not 403 or 401) so user B cannot tell the item exists,
// and the row in the DB is left untouched.
func TestHandleSetItemStatusOtherUserItemReturns404(t *testing.T) {
	srv, _, cleanup := newStatusIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	// User A owns the item.
	aliceID, itemID := seedStatusItem(t, srv.store, ctx, "owner-alice")
	// User B is a second tenant.
	bobID := seedNamedUser(t, srv.store, ctx, "status-bob")

	// User B tries to PATCH alice's item.
	req := newStatusPatchRequest(t, itemID, `{"status":"archived"}`)
	req = req.WithContext(auth.ContextWithUser(req.Context(),
		auth.User{ID: bobID}))
	rr := httptest.NewRecorder()

	srv.handleSetItemStatus(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (NFR 2.1: tenant boundary), got %d (body=%q)",
			rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not_found") {
		t.Fatalf("expected not_found body, got %q", rr.Body.String())
	}

	// The DB row is unchanged — defense-in-depth verification that the
	// UPDATE in store.UpdateItemStatus filtered on user_id as well as id.
	var got string
	if err := srv.store.DB.QueryRow(ctx,
		`SELECT status FROM items WHERE id = $1`, itemID).Scan(&got); err != nil {
		t.Fatalf("post-attempt SELECT failed: %v", err)
	}
	if got != store.ItemStatusUnread {
		t.Errorf("items.status = %q after cross-tenant PATCH attempt, want %q (alice's row was mutated by bob)",
			got, store.ItemStatusUnread)
	}
	// Sanity check we are looking at alice's row.
	var ownerID string
	if err := srv.store.DB.QueryRow(ctx,
		`SELECT user_id FROM items WHERE id = $1`, itemID).Scan(&ownerID); err != nil {
		t.Fatalf("ownership SELECT failed: %v", err)
	}
	if ownerID != aliceID {
		t.Fatalf("test setup invariant: item belongs to %q, not alice (%q)",
			ownerID, aliceID)
	}
}
