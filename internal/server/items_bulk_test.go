package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// fakeBulkStore is the test seam used to substitute the production
// *store.Store dependency of handleBulkDeleteItems / handleBulkTagItems.
// Tests assign deleteFn / tagFn to script the response and record the
// arguments the handler actually passed in, so authorization-collapse
// and partial-failure behaviour can be observed without a database.
type fakeBulkStore struct {
	deleteFn      func(ctx context.Context, userID string, itemIDs []string) ([]string, error)
	tagFn         func(ctx context.Context, userID string, itemIDs []string, tagInput store.TagInput) ([]store.BulkTagResult, error)
	lastDeleteIDs []string
	lastTagIDs    []string
	lastTagInput  store.TagInput
	deleteCalled  bool
	tagCalled     bool
}

func (f *fakeBulkStore) BulkDeleteItems(ctx context.Context, userID string, itemIDs []string) ([]string, error) {
	f.deleteCalled = true
	f.lastDeleteIDs = append([]string(nil), itemIDs...)
	if f.deleteFn == nil {
		return []string{}, nil
	}
	return f.deleteFn(ctx, userID, itemIDs)
}

func (f *fakeBulkStore) BulkAddItemTag(ctx context.Context, userID string, itemIDs []string, tagInput store.TagInput) ([]store.BulkTagResult, error) {
	f.tagCalled = true
	f.lastTagIDs = append([]string(nil), itemIDs...)
	f.lastTagInput = tagInput
	if f.tagFn == nil {
		return []store.BulkTagResult{}, nil
	}
	return f.tagFn(ctx, userID, itemIDs, tagInput)
}

// newBulkTestServer returns a Server suitable for direct handler-level
// testing of the bulk endpoints. logger goes to discard so log lines do
// not pollute `go test` output; tests that exercise log structure
// install their own buffered handler with setBufferedLogger().
func newBulkTestServer() (*Server, *fakeBulkStore) {
	fake := &fakeBulkStore{}
	cfg := config.Config{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{
		cfg:       cfg,
		limiter:   ratelimit.New(60, 60),
		logger:    logger,
		bulkStore: fake,
	}
	return s, fake
}

// newRateLimitedBulkTestServer returns a Server whose limiter
// immediately rejects every call. ratelimit.New(0, 0) sets rate=0 and
// burst=0, so the first Allow() returns false (tokens<1 from the
// outset) — exactly the regression-fix shape for the 429 path
// (tasks.md task 3 / NFR 2.1 rate-limit retention).
func newRateLimitedBulkTestServer() (*Server, *fakeBulkStore) {
	fake := &fakeBulkStore{}
	cfg := config.Config{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{
		cfg:       cfg,
		limiter:   ratelimit.New(0, 0),
		logger:    logger,
		bulkStore: fake,
	}
	return s, fake
}

// setBufferedLogger replaces s.logger with a slog.Logger writing into
// the returned bytes.Buffer. Tests that need to assert log structure
// (NFR 5.1: user_id / item_ids / succeeded_count / failed_count /
// failed_ids / request_id present, secrets absent) parse the buffered
// JSON output line-by-line.
func setBufferedLogger(s *Server) *bytes.Buffer {
	buf := &bytes.Buffer{}
	s.logger = slog.New(slog.NewJSONHandler(buf, nil))
	return buf
}

// withAuthUser returns r with the canonical authn context attached so
// the handlers can read the user via auth.UserFromContext (the same
// pattern existing extension_contract_test.go / items_status_test.go
// rely on). userID maps onto user.ID, the field the handlers actually
// inspect for rate limiting / logging.
func withAuthUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(auth.ContextWithUser(r.Context(), auth.User{ID: userID}))
}

// ---------------------------------------------------------------
// Delete handler suite (tasks.md task 3 / 11 cases)
// ---------------------------------------------------------------

func TestHandleBulkDeleteItems_UnauthorizedReturnsJSON401(t *testing.T) {
	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete",
		strings.NewReader(`{"item_ids":["00000000-0000-0000-0000-000000000001"]}`))
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %q", rr.Body.String())
	}
}

func TestHandleBulkDeleteItems_InvalidJSONReturns400(t *testing.T) {
	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete",
		strings.NewReader(`{not-json`))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}

func TestHandleBulkDeleteItems_EmptyIDsReturns400(t *testing.T) {
	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete",
		strings.NewReader(`{"item_ids":[]}`))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}

func TestHandleBulkDeleteItems_OverLimitReturns400PayloadTooLarge(t *testing.T) {
	// 101 short (length-only) valid-UUID strings would push body bytes
	// over the cap; reuse the same short canonical UUID 101 times to
	// keep the body under the byte cap so that the element-count gate
	// fires (NFR 2.1 server enforcement of the 100-item ceiling).
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
	}
	body, err := json.Marshal(map[string]any{"item_ids": ids})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The body is ~ 101 * 38 + envelope ~ 3.9 KiB, well below the 16 KiB cap.

	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", bytes.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large body, got %q", rr.Body.String())
	}
}

func TestHandleBulkDeleteItems_RejectsBearerAuthReturns403(t *testing.T) {
	s, fake := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete",
		strings.NewReader(`{"item_ids":["00000000-0000-0000-0000-000000000001"]}`))
	// Simulate a Bearer JWT making it past the requireAuth middleware:
	// auth context is populated (handler is reached) AND Authorization
	// header is non-empty. Handler must reject with 403 forbidden so
	// the extension / MCP surface stays single-item per requirements.md
	// Out of Scope.
	req.Header.Set("Authorization", "Bearer test-jwt-token")
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "forbidden") {
		t.Fatalf("expected forbidden body, got %q", rr.Body.String())
	}
	if fake.deleteCalled {
		t.Fatal("store BulkDeleteItems must not be reached for Bearer-authenticated requests")
	}
}

func TestHandleBulkDeleteItems_RateLimitedReturns429(t *testing.T) {
	s, fake := newRateLimitedBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete",
		strings.NewReader(`{"item_ids":["00000000-0000-0000-0000-000000000001"]}`))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "rate_limited") {
		t.Fatalf("expected rate_limited body, got %q", rr.Body.String())
	}
	if fake.deleteCalled {
		t.Fatal("store must not be called when rate limit triggers")
	}
}

func TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	// fake echoes back whatever ids were passed (i.e. the valid UUID).
	fake.deleteFn = func(ctx context.Context, userID string, ids []string) ([]string, error) {
		return append([]string(nil), ids...), nil
	}

	body := `{"item_ids":["not-a-uuid","00000000-0000-0000-0000-000000000001"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", strings.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkDeleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rr.Body.String())
	}
	if len(resp.Succeeded) != 1 || resp.Succeeded[0] != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected succeeded=[valid-uuid], got %#v", resp.Succeeded)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ItemID != "not-a-uuid" || resp.Failed[0].Reason != "not_found" {
		t.Fatalf("expected failed=[{not-a-uuid, not_found}], got %#v", resp.Failed)
	}
	// Critical Req 8.3 / Security Considerations regression fix: the
	// invalid string must NOT have reached the store layer.
	for _, id := range fake.lastDeleteIDs {
		if id == "not-a-uuid" {
			t.Fatalf("invalid uuid string leaked to store: %q (full args: %#v)",
				id, fake.lastDeleteIDs)
		}
	}
}

func TestHandleBulkDeleteItems_PartialFailureResponse_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	// Three ids requested, store reports only the first two as
	// succeeded — simulating that the third id belongs to another user
	// or has already been deleted.
	id1 := "00000000-0000-0000-0000-000000000001"
	id2 := "00000000-0000-0000-0000-000000000002"
	id3 := "00000000-0000-0000-0000-000000000003"
	fake.deleteFn = func(ctx context.Context, userID string, ids []string) ([]string, error) {
		return []string{ids[0], ids[1]}, nil
	}

	body := fmt.Sprintf(`{"item_ids":[%q,%q,%q]}`, id1, id2, id3)
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", strings.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkDeleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 2 {
		t.Fatalf("expected 2 succeeded, got %d (%#v)", len(resp.Succeeded), resp.Succeeded)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ItemID != id3 || resp.Failed[0].Reason != "not_found" {
		t.Fatalf("expected failed=[{%s, not_found}], got %#v", id3, resp.Failed)
	}

	// Regression fix for Req 4.7 / 4.8 / 8.2 / 8.3: title / url MUST
	// NOT appear anywhere in the JSON response (the struct does not
	// have those fields, but pin it at the wire-level so a future
	// refactor cannot reintroduce the leak).
	rawJSON := rr.Body.String()
	if strings.Contains(rawJSON, `"title"`) {
		t.Fatalf("response leaked title field: %q", rawJSON)
	}
	if strings.Contains(rawJSON, `"url"`) {
		t.Fatalf("response leaked url field: %q", rawJSON)
	}
}

func TestHandleBulkDeleteItems_StoreErrorReturns500DBError_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	fake.deleteFn = func(ctx context.Context, userID string, ids []string) ([]string, error) {
		return nil, errors.New("connection lost")
	}

	body := `{"item_ids":["00000000-0000-0000-0000-000000000001"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", strings.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "db_error") {
		t.Fatalf("expected db_error body, got %q", rr.Body.String())
	}
	// Per-item report MUST NOT leak through on store-error path; only
	// the canonical 500 db_error envelope should be present (design.md
	// "部分失敗時の atomicity 方針" 節).
	if strings.Contains(rr.Body.String(), `"failed"`) {
		t.Fatalf("store error path must not include failed[] response: %q", rr.Body.String())
	}
}

func TestHandleBulkDeleteItems_LogsStructuredFields_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	buf := setBufferedLogger(s)
	fake.deleteFn = func(ctx context.Context, userID string, ids []string) ([]string, error) {
		return append([]string(nil), ids...), nil
	}

	body := `{"item_ids":["00000000-0000-0000-0000-000000000001"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", strings.NewReader(body))
	req.Header.Set("Cookie", "altpocket_session=super-secret-cookie")
	req = withAuthUser(req, "user-42")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	logLine := buf.String()
	for _, want := range []string{
		`"msg":"items.bulk.delete"`,
		`"user_id":"user-42"`,
		`"item_ids":`,
		`"succeeded_count":1`,
		`"failed_count":0`,
		`"failed_ids":`,
		`"request_id"`,
	} {
		if !strings.Contains(logLine, want) {
			t.Errorf("log line missing %q (got=%s)", want, logLine)
		}
	}
	// Critical NFR 5.1: secrets must NOT appear in the structured log.
	for _, forbidden := range []string{
		"super-secret-cookie",
		"Bearer",
		"altpocket_session",
		"Authorization",
	} {
		if strings.Contains(logLine, forbidden) {
			t.Errorf("log line leaked secret %q (got=%s)", forbidden, logLine)
		}
	}
}

func TestHandleBulkDeleteItems_RequestBodyExceedsByteLimitReturns400PayloadTooLarge(t *testing.T) {
	// Build a JSON body whose syntax is valid but whose total size
	// exceeds maxBulkRequestBodyBytes. ~500 distinct UUID literals at
	// roughly 39 bytes apiece pushes the body north of 19 KiB, well
	// over the 16 KiB cap. The byte cap MUST fire before the element-
	// count gate (the >100 element check fires at item 101, but the
	// MaxBytesReader stop occurs mid-decode when the cumulative read
	// size crosses the 16 KiB threshold) so this test pins the byte-
	// level DoS-face cap in the validation chain (design.md "Request
	// Size Cap" 節).
	ids := make([]string, 500)
	for i := range ids {
		// Each id is a valid UUID-like string (the JSON decoder reads
		// it byte-by-byte; UUID-format validation only runs on the
		// decoded slice).
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
	}
	body, err := json.Marshal(map[string]any{"item_ids": ids})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(body) <= maxBulkRequestBodyBytes {
		t.Fatalf("test fixture under-shot the cap: body=%d bytes, cap=%d", len(body), maxBulkRequestBodyBytes)
	}

	s, fake := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-delete", bytes.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkDeleteItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large body, got %q", rr.Body.String())
	}
	if fake.deleteCalled {
		t.Fatal("store must not be called when MaxBytesReader cap fires")
	}
}

// ---------------------------------------------------------------
// Tag handler suite (tasks.md task 3 / 13 cases)
// ---------------------------------------------------------------

func TestHandleBulkTagItems_UnauthorizedReturnsJSON401(t *testing.T) {
	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag",
		strings.NewReader(`{"item_ids":["00000000-0000-0000-0000-000000000001"],"tag":"go"}`))
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %q", rr.Body.String())
	}
}

func TestHandleBulkTagItems_InvalidJSONReturns400(t *testing.T) {
	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag",
		strings.NewReader(`{not-json`))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}

func TestHandleBulkTagItems_EmptyIDsReturns400(t *testing.T) {
	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag",
		strings.NewReader(`{"item_ids":[],"tag":"go"}`))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}

func TestHandleBulkTagItems_OverLimitReturns400PayloadTooLarge(t *testing.T) {
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
	}
	body, err := json.Marshal(map[string]any{"item_ids": ids, "tag": "go"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", bytes.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large body, got %q", rr.Body.String())
	}
}

func TestHandleBulkTagItems_RejectsBearerAuthReturns403(t *testing.T) {
	s, fake := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag",
		strings.NewReader(`{"item_ids":["00000000-0000-0000-0000-000000000001"],"tag":"go"}`))
	req.Header.Set("Authorization", "Bearer test-jwt-token")
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "forbidden") {
		t.Fatalf("expected forbidden body, got %q", rr.Body.String())
	}
	if fake.tagCalled {
		t.Fatal("store BulkAddItemTag must not be reached for Bearer-authenticated requests")
	}
}

func TestHandleBulkTagItems_RateLimitedReturns429(t *testing.T) {
	s, fake := newRateLimitedBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag",
		strings.NewReader(`{"item_ids":["00000000-0000-0000-0000-000000000001"],"tag":"go"}`))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "rate_limited") {
		t.Fatalf("expected rate_limited body, got %q", rr.Body.String())
	}
	if fake.tagCalled {
		t.Fatal("store must not be called when rate limit triggers")
	}
}

func TestHandleBulkTagItems_EmptyTagReturns400InvalidTag(t *testing.T) {
	// All three shapes must surface as invalid_tag — never
	// invalid_request — so the client can dispatch the failure into
	// the tag input focus path (Req 5.9 / design.md Error Categories
	// 節). Each case carries at least one valid item_id so the
	// item_ids gate is a no-op and validation reaches the tag check.
	const validUUID = `"00000000-0000-0000-0000-000000000001"`
	cases := []struct {
		name string
		body string
	}{
		{name: "whitespace tag", body: `{"item_ids":[` + validUUID + `],"tag":"   "}`},
		{name: "empty tag string", body: `{"item_ids":[` + validUUID + `],"tag":""}`},
		{name: "missing tag field", body: `{"item_ids":[` + validUUID + `]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newBulkTestServer()
			req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag",
				strings.NewReader(tc.body))
			req = withAuthUser(req, "user-1")
			rr := httptest.NewRecorder()

			s.handleBulkTagItems(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "invalid_tag") {
				t.Fatalf("expected invalid_tag body, got %q", rr.Body.String())
			}
			// Critical: must NOT collapse to invalid_request — the
			// client uses the distinct category to keep the dialog
			// open and focus the input.
			if strings.Contains(rr.Body.String(), "invalid_request") {
				t.Fatalf("empty tag must NOT collapse to invalid_request, got %q", rr.Body.String())
			}
		})
	}
}

func TestHandleBulkTagItems_NormalizationEmptyTagReturns400InvalidTag(t *testing.T) {
	// Full-width space + ASCII space — both collapse to empty after
	// tag.Normalize (NFKC -> ASCII space -> trim). This pins the
	// server-side dual-defense: the JS layer also normalizes before
	// fetch, but the server must catch escapees just the same.
	body := `{"item_ids":["00000000-0000-0000-0000-000000000001"],"tag":"　 "}`
	s, _ := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", strings.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_tag") {
		t.Fatalf("expected invalid_tag body, got %q", rr.Body.String())
	}
}

func TestHandleBulkTagItems_InvalidUUIDsCollapseToFailedNotFound_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	// Fake echoes back the valid id as succeeded with one tag.
	fake.tagFn = func(ctx context.Context, userID string, ids []string, ti store.TagInput) ([]store.BulkTagResult, error) {
		results := make([]store.BulkTagResult, 0, len(ids))
		for _, id := range ids {
			results = append(results, store.BulkTagResult{
				ItemID: id,
				Tags:   []store.Tag{{ID: "tag-1", Name: ti.Name, NormalizedName: ti.NormalizedName}},
			})
		}
		return results, nil
	}

	body := `{"item_ids":["not-a-uuid","00000000-0000-0000-0000-000000000001"],"tag":"GoLang"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", strings.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkTagResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 1 || resp.Succeeded[0].ItemID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected one succeeded with valid uuid, got %#v", resp.Succeeded)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ItemID != "not-a-uuid" || resp.Failed[0].Reason != "not_found" {
		t.Fatalf("expected failed=[{not-a-uuid, not_found}], got %#v", resp.Failed)
	}
	for _, id := range fake.lastTagIDs {
		if id == "not-a-uuid" {
			t.Fatalf("invalid uuid string leaked to store: %q (full args: %#v)",
				id, fake.lastTagIDs)
		}
	}
}

func TestHandleBulkTagItems_PartialFailureResponse_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	id1 := "00000000-0000-0000-0000-000000000001"
	id2 := "00000000-0000-0000-0000-000000000002"
	id3 := "00000000-0000-0000-0000-000000000003"
	// Store only returns succeeded for the first two ids.
	fake.tagFn = func(ctx context.Context, userID string, ids []string, ti store.TagInput) ([]store.BulkTagResult, error) {
		return []store.BulkTagResult{
			{ItemID: ids[0], Tags: []store.Tag{{ID: "tag-1", Name: ti.Name, NormalizedName: ti.NormalizedName}}},
			{ItemID: ids[1], Tags: []store.Tag{{ID: "tag-1", Name: ti.Name, NormalizedName: ti.NormalizedName}}},
		}, nil
	}

	body := fmt.Sprintf(`{"item_ids":[%q,%q,%q],"tag":"go"}`, id1, id2, id3)
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", strings.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	var resp BulkTagResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Succeeded) != 2 {
		t.Fatalf("expected 2 succeeded, got %d (%#v)", len(resp.Succeeded), resp.Succeeded)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ItemID != id3 || resp.Failed[0].Reason != "not_found" {
		t.Fatalf("expected failed=[{%s, not_found}], got %#v", id3, resp.Failed)
	}
	// Same wire-level leak guard as the delete variant.
	rawJSON := rr.Body.String()
	if strings.Contains(rawJSON, `"title"`) {
		t.Fatalf("response leaked title field: %q", rawJSON)
	}
	if strings.Contains(rawJSON, `"url"`) {
		t.Fatalf("response leaked url field: %q", rawJSON)
	}
}

func TestHandleBulkTagItems_StoreErrorReturns500DBError_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	fake.tagFn = func(ctx context.Context, userID string, ids []string, ti store.TagInput) ([]store.BulkTagResult, error) {
		return nil, errors.New("connection lost")
	}

	body := `{"item_ids":["00000000-0000-0000-0000-000000000001"],"tag":"go"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", strings.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "db_error") {
		t.Fatalf("expected db_error body, got %q", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"failed"`) {
		t.Fatalf("store error path must not include failed[] response: %q", rr.Body.String())
	}
}

func TestHandleBulkTagItems_LogsStructuredFields_FakeStore(t *testing.T) {
	s, fake := newBulkTestServer()
	buf := setBufferedLogger(s)
	fake.tagFn = func(ctx context.Context, userID string, ids []string, ti store.TagInput) ([]store.BulkTagResult, error) {
		results := make([]store.BulkTagResult, 0, len(ids))
		for _, id := range ids {
			results = append(results, store.BulkTagResult{
				ItemID: id,
				Tags:   []store.Tag{{ID: "tag-1", Name: ti.Name, NormalizedName: ti.NormalizedName}},
			})
		}
		return results, nil
	}

	body := `{"item_ids":["00000000-0000-0000-0000-000000000001"],"tag":"GoLang"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", strings.NewReader(body))
	req.Header.Set("Cookie", "altpocket_session=super-secret-cookie")
	req = withAuthUser(req, "user-42")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	logLine := buf.String()
	for _, want := range []string{
		`"msg":"items.bulk.tag"`,
		`"user_id":"user-42"`,
		`"item_ids":`,
		`"tag_normalized":"golang"`,
		`"succeeded_count":1`,
		`"failed_count":0`,
		`"failed_ids":`,
		`"request_id"`,
	} {
		if !strings.Contains(logLine, want) {
			t.Errorf("log line missing %q (got=%s)", want, logLine)
		}
	}
	for _, forbidden := range []string{
		"super-secret-cookie",
		"Bearer",
		"altpocket_session",
		"Authorization",
	} {
		if strings.Contains(logLine, forbidden) {
			t.Errorf("log line leaked secret %q (got=%s)", forbidden, logLine)
		}
	}
}

func TestHandleBulkTagItems_RequestBodyExceedsByteLimitReturns400PayloadTooLarge(t *testing.T) {
	// The tag field is the easiest knob to push the body over the cap:
	// a single valid item_ids entry + a tag value that, once
	// JSON-encoded, lands above 16 KiB. We feed strings.Repeat(...) of
	// the literal byte cap so that the encoded body (the value alone
	// is exactly `maxBulkRequestBodyBytes` characters, plus envelope)
	// is reliably over the cap.
	hugeTag := strings.Repeat("a", maxBulkRequestBodyBytes)
	body, err := json.Marshal(map[string]any{
		"item_ids": []string{"00000000-0000-0000-0000-000000000001"},
		"tag":      hugeTag,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(body) <= maxBulkRequestBodyBytes {
		t.Fatalf("fixture under-shot the cap: body=%d, cap=%d", len(body), maxBulkRequestBodyBytes)
	}

	s, fake := newBulkTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/bulk-tag", bytes.NewReader(body))
	req = withAuthUser(req, "user-1")
	rr := httptest.NewRecorder()

	s.handleBulkTagItems(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large body, got %q", rr.Body.String())
	}
	if fake.tagCalled {
		t.Fatal("store must not be called when MaxBytesReader cap fires")
	}
}

// ---------------------------------------------------------------
// Route registration (tasks.md task 3 / 1 case)
// ---------------------------------------------------------------

func TestBulkRoutesRegisteredOnRouter(t *testing.T) {
	// The route table is built by s.Routes() with the full requireAuth /
	// CSRF / cors middleware chain. We assert that the two bulk endpoints
	// are present and registered as POST handlers using chi's Walk.
	//
	// The `/{id}` wildcard route under /v1/items must not eclipse the
	// new static segments — chi v5 prefers static prefixes, so the
	// existence of a POST handler at the static path is the regression
	// signal for the routing tree shape (design.md "Routing Glue" 節).
	s := newAuthTestServer()
	// authTestServer.bulkStore is nil because newAuthTestServer used
	// New(...) before bulkStore wiring landed in this PR; assign here
	// so the router build does not panic on chi.Walk visiting the
	// handler closure (chi only registers the handler; it does not
	// invoke it during Walk).
	if s.bulkStore == nil {
		s.bulkStore = &fakeBulkStore{}
	}
	handler := s.Routes()
	router, ok := handler.(chi.Router)
	if !ok {
		t.Fatalf("Routes() returned %T, want chi.Router", handler)
	}

	got := map[string]bool{}
	walkErr := chi.Walk(router, func(method, route string, h http.Handler, ms ...func(http.Handler) http.Handler) error {
		got[method+" "+route] = true
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk failed: %v", walkErr)
	}

	for _, want := range []string{
		"POST /v1/items/bulk-delete",
		"POST /v1/items/bulk-tag",
	} {
		if !got[want] {
			t.Fatalf("expected route %q to be registered, got routes: %#v", want, got)
		}
	}
}
