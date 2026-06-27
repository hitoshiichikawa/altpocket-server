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

// These tests exercise the bulk-delete / bulk-tag handlers against a real
// PostgreSQL database. They are gated by `-tags=integration` and the
// TEST_DATABASE_URL env var so they do NOT run in the default
// `go test ./...` invocation (task 4 / tasks.md).
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/server/...
//
// The test database must have schema migrations 001..007 applied. Tests use
// per-test labelled users (seedBulkIntegUser) so the shared TEST_DATABASE_URL
// stays safe under concurrent runs (multiple labels also bypass the
// users.google_sub UNIQUE collision that two unlabelled seedTestUser calls
// inside one test would hit). The pattern mirrors
// internal/store/items_bulk_test.go's seedBulkUser helper to avoid coupling
// the server-layer integration test to the store-package test fixtures.

// newBulkIntegrationServer builds a Server backed by a real *store.Store
// and a buffered JSON slog handler. The buffer lets the structured-log
// tests (NFR 5.1) assert that the canonical 6 fields are present and that
// the cookie / Authorization values do NOT leak.
func newBulkIntegrationServer(t *testing.T) (*Server, *bytes.Buffer, func()) {
	t.Helper()
	st, cleanup := newIntegrationStore(t)
	buf := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Config{PublicBaseURL: "https://www.example.invalid"}
	s := New(cfg, st, ratelimit.New(60, 60), logger, nil)
	return s, buf, cleanup
}

// seedBulkIntegUser creates a throwaway user row with a unique label so a
// single test can seed multiple users (caller vs other-user) without
// hitting the users.google_sub UNIQUE constraint. Cleanup deletes the user
// row (and cascades to items / item_tags via FK) at test end.
func seedBulkIntegUser(t *testing.T, s *store.Store, ctx context.Context, label string) string {
	t.Helper()
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO users (google_sub, email, name)
		VALUES ($1, $2, $3)
		RETURNING id
	`, "test-sub-"+t.Name()+"-"+label, t.Name()+"-"+label+"@example.invalid", t.Name()+"-"+label).Scan(&id)
	if err != nil {
		t.Fatalf("seed user %q: %v", label, err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// seedBulkIntegItem inserts a single items row for the given user using a
// hash derived from the test name + label so multiple items per user do not
// collide on the items.UNIQUE (user_id, canonical_hash) constraint.
func seedBulkIntegItem(t *testing.T, s *store.Store, ctx context.Context, userID, hash string) string {
	t.Helper()
	scopedHash := t.Name() + "-" + hash
	var itemID string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO items (user_id, url, canonical_url, canonical_hash, title)
		VALUES ($1, $2, $2, $3, $4)
		RETURNING id
	`, userID, "https://example.invalid/"+scopedHash, scopedHash, "title-"+hash).Scan(&itemID)
	if err != nil {
		t.Fatalf("seed item %q: %v", hash, err)
	}
	return itemID
}

// seedBulkIntegItemWithTag mirrors seedBulkItemWithTag from
// internal/store/items_bulk_test.go: it goes through the real CreateItem
// path so the per-user display name (item_tags.display_name) is persisted
// exactly the same way production would. The NOT EXISTS orphan-tag cleanup
// keeps the shared TEST_DATABASE_URL safe under concurrent runs.
func seedBulkIntegItemWithTag(t *testing.T, s *store.Store, ctx context.Context, userID, hash, displayTagName string) string {
	t.Helper()
	inputs := normalizeTagInputs([]string{displayTagName})
	if len(inputs) == 0 {
		t.Fatalf("normalizeTagInputs returned empty for %q", displayTagName)
	}
	scopedHash := t.Name() + "-" + hash
	rawURL := "https://example.invalid/" + scopedHash
	itemID, _, err := s.CreateItem(ctx, userID, rawURL, rawURL, scopedHash, inputs, "title-"+hash, "")
	if err != nil {
		t.Fatalf("CreateItem %q: %v", hash, err)
	}
	normalized := inputs[0].NormalizedName
	t.Cleanup(func() {
		// Bounded orphan-tag cleanup: the global tags.normalized_name UNIQUE
		// is shared across users / concurrent tests, so only delete the row
		// when no item_tags references remain (PR #137 round 6 pattern).
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, normalized)
	})
	return itemID
}

// withAuthUserCtx attaches the canonical authn context. Mirrors
// withAuthUser in items_bulk_test.go but is duplicated here so the
// integration suite stays compilable on its own under -tags=integration.
func withAuthUserCtx(r *http.Request, userID string) *http.Request {
	return r.WithContext(auth.ContextWithUser(r.Context(), auth.User{ID: userID}))
}

// findLogRecord parses every JSON line in buf and returns the first one
// whose "msg" field equals msg. Returns nil if not found.
func findLogRecord(t *testing.T, buf *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not JSON: %v (line=%q)", err, line)
		}
		if s, _ := rec["msg"].(string); s == msg {
			return rec
		}
	}
	return nil
}

// ---------------------------------------------------------------
// Bulk-delete suite
// ---------------------------------------------------------------

// TestHandleBulkDeleteItems_PartialFailureResponse covers Req 4.5 / 4.7 /
// 4.8 / 8.1 / 8.2 / 8.3 against the real DB: the caller submits 3 own +
// 2 other-user + 1 syntactically-valid missing UUID, and the response
// must report succeeded=[own 3] / failed=[other 2 + missing 1] with
// reason="not_found" for every failure. The wire-level assertion that
// `"title"` / `"url"` never appear in the response body pins Req 8.2 /
// 8.3 leak prevention against future refactors of BulkFailureDetail.
func TestHandleBulkDeleteItems_PartialFailureResponse(t *testing.T) {
	srv, _, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	otherID := seedBulkIntegUser(t, srv.store, ctx, "other")

	own1 := seedBulkIntegItem(t, srv.store, ctx, callerID, "own1")
	own2 := seedBulkIntegItem(t, srv.store, ctx, callerID, "own2")
	own3 := seedBulkIntegItem(t, srv.store, ctx, callerID, "own3")
	other1 := seedBulkIntegItem(t, srv.store, ctx, otherID, "other1")
	other2 := seedBulkIntegItem(t, srv.store, ctx, otherID, "other2")
	missing := "11111111-1111-1111-1111-111111111111"

	bodyJSON, err := json.Marshal(map[string]any{
		"item_ids": []string{own1, own2, own3, other1, other2, missing},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", bytes.NewReader(bodyJSON))
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}

	var resp BulkDeleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rr.Body.String())
	}

	succeededSet := map[string]bool{}
	for _, id := range resp.Succeeded {
		succeededSet[id] = true
	}
	for _, want := range []string{own1, own2, own3} {
		if !succeededSet[want] {
			t.Errorf("succeeded missing own id %q (got %v)", want, resp.Succeeded)
		}
	}
	if len(resp.Succeeded) != 3 {
		t.Errorf("succeeded len = %d, want 3 (got %v)", len(resp.Succeeded), resp.Succeeded)
	}

	failedByID := map[string]string{}
	for _, f := range resp.Failed {
		failedByID[f.ItemID] = f.Reason
	}
	for _, want := range []string{other1, other2, missing} {
		reason, ok := failedByID[want]
		if !ok {
			t.Errorf("failed missing id %q (got %v)", want, resp.Failed)
			continue
		}
		if reason != "not_found" {
			t.Errorf("failed[%q] reason = %q, want %q", want, reason, "not_found")
		}
	}
	if len(resp.Failed) != 3 {
		t.Errorf("failed len = %d, want 3 (got %v)", len(resp.Failed), resp.Failed)
	}

	// Wire-level leak guard: the BulkFailureDetail struct intentionally
	// does NOT carry title / url, so neither field name must appear in
	// the JSON response (Req 4.7 / 4.8 / 8.2 / 8.3).
	rawJSON := rr.Body.String()
	if strings.Contains(rawJSON, `"title"`) {
		t.Errorf("response leaked title field: %q", rawJSON)
	}
	if strings.Contains(rawJSON, `"url"`) {
		t.Errorf("response leaked url field: %q", rawJSON)
	}

	// DB-side cross check: other-user items must remain in DB (Req 8.2).
	var existsOther1, existsOther2 bool
	if err := srv.store.DB.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM items WHERE id = $1)`, other1).Scan(&existsOther1); err != nil {
		t.Fatalf("post-call SELECT other1: %v", err)
	}
	if err := srv.store.DB.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM items WHERE id = $1)`, other2).Scan(&existsOther2); err != nil {
		t.Fatalf("post-call SELECT other2: %v", err)
	}
	if !existsOther1 || !existsOther2 {
		t.Errorf("other-user items must remain in DB after caller's bulk delete: other1=%v other2=%v",
			existsOther1, existsOther2)
	}
}

// TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound pins Req
// 8.3 / Security Considerations 節 DB エラー誘発攻撃面遮断 at the real
// DB layer (task 3 covered the fake-store side). The handler must
// partition the request slice into valid vs invalid UUID strings, send
// only valid ids to the store, and surface the invalid one as
// failed[{reason:"not_found"}]. The endpoint must NOT 500 — that would
// be a regression of the "invalid uuid is collapsed to not_found"
// contract.
func TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound(t *testing.T) {
	srv, _, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	own := seedBulkIntegItem(t, srv.store, ctx, callerID, "own")

	body, err := json.Marshal(map[string]any{
		"item_ids": []string{"not-a-uuid", own},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", bytes.NewReader(body))
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q) — invalid UUID must NOT trigger 500 db_error", rr.Code, rr.Body.String())
	}

	var resp BulkDeleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 1 || resp.Succeeded[0] != own {
		t.Errorf("succeeded = %v, want [%s]", resp.Succeeded, own)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ItemID != "not-a-uuid" || resp.Failed[0].Reason != "not_found" {
		t.Errorf("failed = %v, want [{not-a-uuid, not_found}]", resp.Failed)
	}
}

// TestHandleBulkTagItems_InvalidUUIDsCollapseToFailedNotFound is the
// bulk-tag counterpart of the delete-side regression above. Invalid UUID
// strings must collapse to failed[{reason:"not_found"}] rather than
// trigger a 500 from the store driver.
func TestHandleBulkTagItems_InvalidUUIDsCollapseToFailedNotFound(t *testing.T) {
	srv, _, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	own := seedBulkIntegItem(t, srv.store, ctx, callerID, "own")

	t.Cleanup(func() {
		_, _ = srv.store.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, "golang")
	})

	body, err := json.Marshal(map[string]any{
		"item_ids": []string{"not-a-uuid", own},
		"tag":      "GoLang",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", bytes.NewReader(body))
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q) — invalid UUID must NOT trigger 500 db_error", rr.Code, rr.Body.String())
	}

	var resp BulkTagResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 1 || resp.Succeeded[0].ItemID != own {
		t.Errorf("succeeded = %v, want one entry for %s", resp.Succeeded, own)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ItemID != "not-a-uuid" || resp.Failed[0].Reason != "not_found" {
		t.Errorf("failed = %v, want [{not-a-uuid, not_found}]", resp.Failed)
	}
}

// TestHandleBulkDeleteItems_AllSuccessResponse covers the no-failure
// happy path: 3 own items → succeeded=3, failed=[], and the structured
// log carries succeeded_count=3 / failed_count=0.
func TestHandleBulkDeleteItems_AllSuccessResponse(t *testing.T) {
	srv, buf, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	own1 := seedBulkIntegItem(t, srv.store, ctx, callerID, "a")
	own2 := seedBulkIntegItem(t, srv.store, ctx, callerID, "b")
	own3 := seedBulkIntegItem(t, srv.store, ctx, callerID, "c")
	buf.Reset()

	body, err := json.Marshal(map[string]any{
		"item_ids": []string{own1, own2, own3},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", bytes.NewReader(body))
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkDeleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 3 {
		t.Errorf("succeeded len = %d, want 3 (got %v)", len(resp.Succeeded), resp.Succeeded)
	}
	if len(resp.Failed) != 0 {
		t.Errorf("failed len = %d, want 0 (got %v)", len(resp.Failed), resp.Failed)
	}

	rec := findLogRecord(t, buf, "items.bulk.delete")
	if rec == nil {
		t.Fatalf("did not find items.bulk.delete log record. captured:\n%s", buf.String())
	}
	// JSON numbers decode to float64 — compare against the float form so
	// the assertion doesn't depend on int / float coercion.
	if got, _ := rec["succeeded_count"].(float64); got != 3 {
		t.Errorf("log succeeded_count = %v, want 3", rec["succeeded_count"])
	}
	if got, _ := rec["failed_count"].(float64); got != 0 {
		t.Errorf("log failed_count = %v, want 0", rec["failed_count"])
	}
}

// TestHandleBulkDeleteItems_LogsStructuredFields pins NFR 5.1 for the
// delete endpoint: the structured log must include user_id / item_ids /
// succeeded_count / failed_count / failed_ids / request_id and MUST NOT
// include the Cookie or Authorization values or the body raw.
func TestHandleBulkDeleteItems_LogsStructuredFields(t *testing.T) {
	srv, buf, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	own := seedBulkIntegItem(t, srv.store, ctx, callerID, "own")
	buf.Reset()

	body, err := json.Marshal(map[string]any{"item_ids": []string{own}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", bytes.NewReader(body))
	// Attach a sensitive-looking session cookie so the negative
	// assertion has something distinctive to grep for in the buffer. We
	// do NOT set Authorization here because the handler returns 403
	// forbidden on any non-empty Authorization (Bearer rejection gate);
	// the 403 path is exercised by items_bulk_test.go and is out of
	// scope for the structured-log assertion.
	req.AddCookie(&http.Cookie{Name: "altpocket_session", Value: "SECRET-SESSION-DO-NOT-LOG"})
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}

	rec := findLogRecord(t, buf, "items.bulk.delete")
	if rec == nil {
		t.Fatalf("did not find items.bulk.delete log record. captured:\n%s", buf.String())
	}

	// Required fields (NFR 5.1).
	for _, want := range []string{
		"user_id", "item_ids", "succeeded_count",
		"failed_count", "failed_ids", "request_id",
	} {
		if _, ok := rec[want]; !ok {
			t.Errorf("log record missing field %q (got keys=%v)", want, recKeys(rec))
		}
	}
	if got, _ := rec["user_id"].(string); got != callerID {
		t.Errorf("log user_id = %q, want %q", got, callerID)
	}

	// Negative assertion across the whole buffer (covers any other log
	// lines emitted during the request lifecycle, not just this record).
	// Authorization / Bearer are intentionally absent from this fixture
	// (the gate rejects them with 403 forbidden) so we focus the
	// forbidden list on Cookie-side leak vectors plus the literal header
	// names so the handler cannot tee the raw request map into the log.
	captured := buf.String()
	for _, forbidden := range []string{
		"SECRET-SESSION-DO-NOT-LOG",
		"altpocket_session",
		"Authorization",
	} {
		if strings.Contains(captured, forbidden) {
			t.Errorf("structured log leaked %q (full buffer:\n%s)", forbidden, captured)
		}
	}
}

// ---------------------------------------------------------------
// Bulk-tag suite
// ---------------------------------------------------------------

// TestHandleBulkTagItems_SucceedsAndReturnsFullTags covers Req 5.3 / 5.4 /
// 5.5: a caller with 2 own items (one pre-tagged with "alpha") tags both
// with "GoLang". The succeeded[] slice must report each item's FULL
// post-update tag set (existing + newly added), so the UI can rerender
// the chip row without an extra fetch.
func TestHandleBulkTagItems_SucceedsAndReturnsFullTags(t *testing.T) {
	srv, _, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	// item1 already has "alpha"; item2 is fresh.
	item1 := seedBulkIntegItemWithTag(t, srv.store, ctx, callerID, "with-alpha", "alpha")
	item2 := seedBulkIntegItem(t, srv.store, ctx, callerID, "fresh")

	t.Cleanup(func() {
		_, _ = srv.store.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, "golang")
	})

	body, err := json.Marshal(map[string]any{
		"item_ids": []string{item1, item2},
		"tag":      "GoLang",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", bytes.NewReader(body))
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkTagResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 2 {
		t.Fatalf("succeeded len = %d, want 2 (%+v)", len(resp.Succeeded), resp.Succeeded)
	}
	if len(resp.Failed) != 0 {
		t.Errorf("failed len = %d, want 0 (%+v)", len(resp.Failed), resp.Failed)
	}

	// Locate item1's entry and assert it carries BOTH alpha (existing)
	// and golang (newly added). For item2 only golang is expected.
	gotByID := map[string][]string{}
	for _, s := range resp.Succeeded {
		names := make([]string, 0, len(s.Tags))
		for _, tg := range s.Tags {
			names = append(names, tg.NormalizedName)
		}
		gotByID[s.ItemID] = names
	}
	if !containsNormalized(gotByID[item1], "alpha") || !containsNormalized(gotByID[item1], "golang") {
		t.Errorf("item1 post-tags = %v, want alpha + golang", gotByID[item1])
	}
	if len(gotByID[item1]) != 2 {
		t.Errorf("item1 post-tags len = %d, want 2 (%v)", len(gotByID[item1]), gotByID[item1])
	}
	if !containsNormalized(gotByID[item2], "golang") {
		t.Errorf("item2 post-tags = %v, want golang", gotByID[item2])
	}
	if len(gotByID[item2]) != 1 {
		t.Errorf("item2 post-tags len = %d, want 1 (%v)", len(gotByID[item2]), gotByID[item2])
	}
}

// TestHandleBulkTagItems_PartialFailureFromOtherUserID covers Req 8.1 /
// 8.2 against the real DB: the caller passes 2 own + 1 other-user id;
// succeeded must contain only the own items, the other-user id surfaces
// as failed[{reason:"not_found"}].
func TestHandleBulkTagItems_PartialFailureFromOtherUserID(t *testing.T) {
	srv, _, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	otherID := seedBulkIntegUser(t, srv.store, ctx, "other")

	own1 := seedBulkIntegItem(t, srv.store, ctx, callerID, "own1")
	own2 := seedBulkIntegItem(t, srv.store, ctx, callerID, "own2")
	other1 := seedBulkIntegItem(t, srv.store, ctx, otherID, "other1")

	t.Cleanup(func() {
		_, _ = srv.store.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, "shared-tag")
	})

	body, err := json.Marshal(map[string]any{
		"item_ids": []string{own1, own2, other1},
		"tag":      "shared-tag",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", bytes.NewReader(body))
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkTagResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}

	succeededIDs := map[string]bool{}
	for _, s := range resp.Succeeded {
		succeededIDs[s.ItemID] = true
	}
	if !succeededIDs[own1] || !succeededIDs[own2] {
		t.Errorf("succeeded must include both own items, got %v", succeededIDs)
	}
	if succeededIDs[other1] {
		t.Errorf("succeeded must NOT include other-user item (cross-tenant leak): %v", succeededIDs)
	}
	if len(resp.Succeeded) != 2 {
		t.Errorf("succeeded len = %d, want 2", len(resp.Succeeded))
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ItemID != other1 || resp.Failed[0].Reason != "not_found" {
		t.Errorf("failed = %v, want [{%s, not_found}]", resp.Failed, other1)
	}

	// DB-side cross check: the other-user item must remain tag-free.
	var otherTagCount int
	if err := srv.store.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM item_tags WHERE item_id = $1`, other1).Scan(&otherTagCount); err != nil {
		t.Fatalf("post-call item_tags SELECT: %v", err)
	}
	if otherTagCount != 0 {
		t.Errorf("other-user item gained %d tag(s) (cross-tenant write leak)", otherTagCount)
	}
}

// TestHandleBulkTagItems_LogsStructuredFields pins NFR 5.1 for the
// bulk-tag endpoint: log includes user_id / item_ids / succeeded_count /
// failed_count / failed_ids / request_id; cookie / Authorization values
// must NOT appear anywhere in the buffer.
func TestHandleBulkTagItems_LogsStructuredFields(t *testing.T) {
	srv, buf, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	own := seedBulkIntegItem(t, srv.store, ctx, callerID, "own")
	buf.Reset()

	t.Cleanup(func() {
		_, _ = srv.store.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, "golang")
	})

	body, err := json.Marshal(map[string]any{
		"item_ids": []string{own},
		"tag":      "GoLang",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "altpocket_session", Value: "SECRET-SESSION-DO-NOT-LOG"})
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}

	rec := findLogRecord(t, buf, "items.bulk.tag")
	if rec == nil {
		t.Fatalf("did not find items.bulk.tag log record. captured:\n%s", buf.String())
	}
	for _, want := range []string{
		"user_id", "item_ids", "succeeded_count",
		"failed_count", "failed_ids", "request_id",
	} {
		if _, ok := rec[want]; !ok {
			t.Errorf("log record missing field %q (got keys=%v)", want, recKeys(rec))
		}
	}
	if got, _ := rec["user_id"].(string); got != callerID {
		t.Errorf("log user_id = %q, want %q", got, callerID)
	}

	captured := buf.String()
	for _, forbidden := range []string{
		"SECRET-SESSION-DO-NOT-LOG",
		"altpocket_session",
		"Authorization",
	} {
		if strings.Contains(captured, forbidden) {
			t.Errorf("structured log leaked %q (full buffer:\n%s)", forbidden, captured)
		}
	}
}

// TestHandleBulkTagItems_DedupesExistingTagInRequest covers Req 5.4 at
// the real DB: an item that already carries the requested tag must
// surface in succeeded with exactly one occurrence of the tag (no
// duplicate row in item_tags). The store's ON CONFLICT DO NOTHING in
// step 4 is the regression target here.
func TestHandleBulkTagItems_DedupesExistingTagInRequest(t *testing.T) {
	srv, _, cleanup := newBulkIntegrationServer(t)
	defer cleanup()
	ctx := context.Background()

	callerID := seedBulkIntegUser(t, srv.store, ctx, "caller")
	// item already carries "shared" — re-tagging with the same value
	// must dedupe.
	item := seedBulkIntegItemWithTag(t, srv.store, ctx, callerID, "with-shared", "shared")

	body, err := json.Marshal(map[string]any{
		"item_ids": []string{item},
		"tag":      "shared",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", bytes.NewReader(body))
	req = withAuthUserCtx(req, callerID)
	rr := httptest.NewRecorder()

	srv.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkTagResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 1 || resp.Succeeded[0].ItemID != item {
		t.Fatalf("succeeded = %+v, want one entry for %s", resp.Succeeded, item)
	}
	// Response must report exactly one tag (no duplicate "shared").
	names := make([]string, 0, len(resp.Succeeded[0].Tags))
	for _, tg := range resp.Succeeded[0].Tags {
		names = append(names, tg.NormalizedName)
	}
	if len(names) != 1 || names[0] != "shared" {
		t.Errorf("response tags = %v, want [shared] (no duplicate)", names)
	}

	// DB-side cross check: item_tags row count for this item must be 1.
	var rowCount int
	if err := srv.store.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM item_tags WHERE item_id = $1`, item).Scan(&rowCount); err != nil {
		t.Fatalf("post-call item_tags SELECT: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("item_tags rows for item = %d, want 1 (dedup regression)", rowCount)
	}
}

// ---------------------------------------------------------------
// Router-level smoke test
// ---------------------------------------------------------------

// TestBulkRoutesOnRealRouterReturnCSRFForbiddenWithoutAuth pins the
// requireAuth middleware ordering: checkCSRF runs BEFORE authenticate,
// so an unauthenticated POST (no Authorization header + no
// altpocket_session cookie + no X-CSRF-Token) is rejected with
// 403 {"error":"csrf"} rather than 401 {"error":"unauthorized"}. Both
// bulk endpoints share the same middleware chain so both must surface
// the same envelope (round 2 review feedback regression固定).
//
// The 401 unauthorized path is not asserted here because it is not
// reached on this fixture; that path is covered by the handler-level
// unit test (TestHandleBulkDeleteItems_UnauthorizedReturnsJSON401 /
// TestHandleBulkTagItems_UnauthorizedReturnsJSON401 in
// items_bulk_test.go).
func TestBulkRoutesOnRealRouterReturnCSRFForbiddenWithoutAuth(t *testing.T) {
	srv, _, cleanup := newBulkIntegrationServer(t)
	defer cleanup()

	handler := srv.Routes()
	router, ok := handler.(chi.Router)
	if !ok {
		t.Fatalf("Routes() returned %T, want chi.Router", handler)
	}

	for _, path := range []string{
		"/v1/items/bulk-delete",
		"/v1/items/bulk-tag",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path,
				strings.NewReader(`{"item_ids":["00000000-0000-0000-0000-000000000001"]}`))
			rr := httptest.NewRecorder()

			router.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected 403 (csrf), got %d (body=%q)", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `"error":"csrf"`) {
				t.Fatalf("expected csrf error envelope, got %q", rr.Body.String())
			}
		})
	}
}

// recKeys returns the sorted keys of a log record for debug output. Used
// only in error messages, so allocation cost is irrelevant.
func recKeys(rec map[string]any) []string {
	keys := make([]string, 0, len(rec))
	for k := range rec {
		keys = append(keys, k)
	}
	return keys
}

// containsNormalized returns true if needle equals any element in
// haystack. Used by the bulk-tag response assertions to test the
// NormalizedName field of a []store.Tag slice without depending on the
// store's ORDER BY producing a specific index.
func containsNormalized(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// _ ensures the store import is retained when the file is compiled
// with -tags=integration but no test references store types directly.
// (The handler response types use store.Tag transitively; this anchor
// keeps the analyzer happy if a future refactor splits the helpers.)
var _ = store.Tag{}
