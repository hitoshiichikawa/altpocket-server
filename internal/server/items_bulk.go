// Package server: items bulk (一括削除 / 一括タグ付け) wiring for Issue #118.
//
// This file holds the HTTP handlers for the two new endpoints:
//
//	POST /v1/items/bulk-delete
//	POST /v1/items/bulk-tag
//
// Both endpoints are session-only (Authorization Bearer JWT is rejected
// at the handler boundary so the拡張 / MCP surface stays single-item
// per requirements.md "Out of Scope: 拡張機能および MCP 経由での一括操作 API 公開")
// and share the same authn / rate-limit / size-cap / per-id UUID
// validation chain. They delegate the actual SQL work to the store
// layer via the bulkItemsStore interface, which *store.Store satisfies
// directly (no adapter needed); the indirection only exists as a test
// seam so that unit tests under `go test ./...` can fake the store and
// observe authorization-collapse / partial-failure / structured-log
// behaviour without an integration database (design.md
// "Handler-side store interface" 節 / round 4 review feedback).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"altpocket/internal/auth"
	"altpocket/internal/store"
	"altpocket/internal/tag"

	"github.com/google/uuid"
)

// Bulk request envelope limits enforced at the server boundary
// (NFR 2.1 + DoS-face cap).
//
//   - maxBulkItemsPerRequest pins the element-count ceiling that the
//     UI also enforces client-side; both layers cap independently so
//     that a malicious / buggy client cannot exceed the chosen 100-item
//     selection (requirements.md NFR 2 と Open Question (c) 確定方針).
//   - maxBulkRequestBodyBytes is the byte-level ceiling applied via
//     http.MaxBytesReader *before* json.Decoder reads the body. With
//     100 UUIDs (36 bytes each) + JSON syntax + the optional `tag`
//     field, ~5 KiB is the theoretical maximum; 16 KiB gives ~3x
//     headroom for HTTP/2 frames and gzip-decode jitter while still
//     hard-stopping OOM-via-decoder probes (design.md "Request Size
//     Cap" 節 / round 4 review feedback).
const (
	maxBulkItemsPerRequest  = 100
	maxBulkRequestBodyBytes = 16 * 1024
)

// bulkItemsStore is the package-private interface that handleBulkDeleteItems
// and handleBulkTagItems use to reach the store layer. *store.Store satisfies
// it implicitly via the matching method signatures, so production wiring is
// just `s.bulkStore = st` in Server.New(). Unit tests substitute a fake
// implementation to observe per-item results without a database.
//
// The interface intentionally keeps the surface area tiny (the two methods
// the bulk handlers actually call) so future refactors of *store.Store
// cannot accidentally drag unrelated dependencies into the handler unit
// tests (design.md "Handler-side store interface" 節 / NFR 3.1〜3.4).
type bulkItemsStore interface {
	BulkDeleteItems(ctx context.Context, userID string, itemIDs []string) (succeeded []string, err error)
	BulkAddItemTag(ctx context.Context, userID string, itemIDs []string, tagInput store.TagInput) (succeeded []store.BulkTagResult, err error)
}

// BulkDeleteRequest is the JSON body schema accepted by POST
// /v1/items/bulk-delete.
type BulkDeleteRequest struct {
	ItemIDs []string `json:"item_ids"`
}

// BulkDeleteResponse is the 200 OK response body for the bulk-delete
// endpoint.
//
// Succeeded carries the item_ids that were actually deleted (other
// users' items / already-deleted items / invalid UUID strings are not
// included). Failed carries each id that did NOT end up in Succeeded,
// collapsed to {item_id, reason: "not_found"} so that ownership /
// existence information cannot leak (design.md "BulkFailureDetail"
// 節 / Security Considerations).
type BulkDeleteResponse struct {
	Succeeded []string            `json:"succeeded"`
	Failed    []BulkFailureDetail `json:"failed"`
}

// BulkTagRequest is the JSON body schema accepted by POST
// /v1/items/bulk-tag. Tag is the single tag string (normalized
// server-side via tag.Normalize for the duplication / empty
// check; the original string is preserved as the display name so
// chip rendering keeps the user-entered casing — Issue #115 / AC 1.3).
type BulkTagRequest struct {
	ItemIDs []string `json:"item_ids"`
	Tag     string   `json:"tag"`
}

// BulkTagResponse is the 200 OK response body for the bulk-tag
// endpoint. Succeeded carries the per-item full updated tag list
// (Req 5.5 — the UI rerenders chip rows from this response without an
// additional fetch). Failed mirrors BulkDeleteResponse semantics.
type BulkTagResponse struct {
	Succeeded []BulkTagSuccessDetail `json:"succeeded"`
	Failed    []BulkFailureDetail    `json:"failed"`
}

// BulkTagSuccessDetail is one entry in BulkTagResponse.Succeeded. The
// Tags slice is the FULL post-update tag set of the item (existing +
// newly added), so the client can rerender the chip row without
// fetching the item again.
type BulkTagSuccessDetail struct {
	ItemID string      `json:"item_id"`
	Tags   []store.Tag `json:"tags"`
}

// BulkFailureDetail is one entry in either BulkDeleteResponse.Failed or
// BulkTagResponse.Failed. Reason is always "not_found" in the v1 of the
// API; ownership / existence / invalid-UUID branches are all collapsed
// to that single value so that no information about other users' items
// can leak through the response (design.md "Components and Interfaces"
// 節の `BulkFailureDetail` 定義 / Req 4.7 / 4.8 / 8.2 / 8.3 / Security
// Considerations節 PII リーク防止).
//
// Title / URL are intentionally NOT included on the response — the
// client side rebuilds the human-readable failure text from the DOM
// (article[data-item-id="<id>"]) so the server never has to choose
// what to disclose. See design.md "失敗 toast の表示文言" 節 and the
// `data-original-url="{{.URL}}"` SSR attribute on items_list.html.
type BulkFailureDetail struct {
	ItemID string `json:"item_id"`
	Reason string `json:"reason"`
}

// handleBulkDeleteItems serves POST /v1/items/bulk-delete (Req 4.x /
// 8.x / NFR 2.1 / NFR 5.1).
//
// The validation order is fixed (and exercised by unit tests in
// items_bulk_test.go) because tests for individual gates rely on
// earlier gates being a no-op for their inputs. In order:
//
//  1. Authorization header rejection (Bearer JWT → 403 forbidden)
//  2. auth context check (handler direct call → 401 unauthorized)
//  3. per-user rate limiter (false → 429 rate_limited)
//  4. http.MaxBytesReader on the request body (byte-level cap)
//  5. JSON decode + body-cap classification (*http.MaxBytesError →
//     400 payload_too_large; other parse error → 400 invalid_request)
//  6. element-count gates (empty → 400 invalid_request, over-100 →
//     400 payload_too_large)
//  7. per-id UUID format check (invalid id is COLLAPSED to
//     failed[{reason: "not_found"}]; only valid ids reach the store)
//  8. store.BulkDeleteItems (err → 500 db_error)
//  9. succeeded / failed assembly and structured log
//
// The 403 forbidden gate is intentionally placed *before* the auth
// context check so an Authorization-bearing request never reaches the
// store / limiter even if the JWT was successfully validated by
// requireAuth's middleware path. This is the goldensource enforcement
// for "拡張機能および MCP 経由での一括操作 API 公開" being out of scope.
func (s *Server) handleBulkDeleteItems(w http.ResponseWriter, r *http.Request) {
	// Gate 1: reject Bearer JWT (extension / MCP) — bulk endpoints are
	// session-only (requirements.md Out of Scope, design.md
	// Architecture Pattern 節 "拡張機能 / MCP Bearer JWT 遮断").
	if r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !s.limiter.Allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}

	// Gate 4 + 5: byte-level cap on the request body, then JSON decode.
	// MaxBytesReader sits ahead of the decoder so the decoder never
	// reads past maxBulkRequestBodyBytes (DoS face / design.md
	// "Request Size Cap" 節). When the cap fires *http.MaxBytesError
	// classifies into payload_too_large; any other decode error is a
	// generic invalid_request.
	r.Body = http.MaxBytesReader(w, r.Body, maxBulkRequestBodyBytes)
	var req BulkDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	if len(req.ItemIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if len(req.ItemIDs) > maxBulkItemsPerRequest {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload_too_large"})
		return
	}

	// Gate 7: per-id UUID format check. Invalid strings never reach
	// the store — they are collapsed into failed[{reason: "not_found"}]
	// so a malicious or buggy client cannot trigger a DB-side error
	// path (design.md Security Considerations / Req 8.3 二重防御).
	validIDs, invalidIDs := partitionByUUID(req.ItemIDs)

	succeeded, err := s.bulkStore.BulkDeleteItems(r.Context(), user.ID, validIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	failed := computeFailedIDs(req.ItemIDs, succeeded, invalidIDs)
	failedDetails := buildFailureDetails(failed)
	failedIDsForLog := failedIDsForLog(failedDetails)

	s.logger.Info("items.bulk.delete",
		slog.String("user_id", user.ID),
		slog.Any("item_ids", req.ItemIDs),
		slog.Int("succeeded_count", len(succeeded)),
		slog.Int("failed_count", len(failedDetails)),
		slog.Any("failed_ids", failedIDsForLog),
		slog.String("request_id", s.requestID(r.Context())))

	writeJSON(w, http.StatusOK, BulkDeleteResponse{
		Succeeded: succeeded,
		Failed:    failedDetails,
	})
}

// handleBulkTagItems serves POST /v1/items/bulk-tag (Req 5.x / 8.x /
// NFR 2.1 / NFR 5.1).
//
// The validation chain mirrors handleBulkDeleteItems with one
// additional gate: after the UUID per-id check (gate 7) and *before*
// the store call, the `tag` field is normalized via tag.Normalize and
// rejected with 400 invalid_tag if the normalized form is empty
// (Req 5.9 server 二重防御). This category is kept distinct from
// invalid_request so the client can route the failure into "focus the
// tag input + show input-validation error" rather than the generic
// "client bug" path (design.md Error Categories 節).
//
// IMPORTANT: empty `item_ids` is rejected before the tag is normalized,
// so a request that omits both fields surfaces as invalid_request, not
// invalid_tag. This is exercised by the test suite (the tag-only tests
// always supply at least one valid UUID) so the dispatch contract is
// pinned (tasks.md line 238-249 / round 2 review feedback).
func (s *Server) handleBulkTagItems(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !s.limiter.Allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBulkRequestBodyBytes)
	var req BulkTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	if len(req.ItemIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if len(req.ItemIDs) > maxBulkItemsPerRequest {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload_too_large"})
		return
	}

	validIDs, invalidIDs := partitionByUUID(req.ItemIDs)

	// Gate 7.5 (bulk-tag only): empty / whitespace-only / NFKC-collapsing
	// tag is the dedicated invalid_tag category (Req 5.9). We use
	// tag.Normalize for the emptiness check because the JS layer
	// (window.altpocketNormalizeTagName) uses the same NFKC+lowercase+trim
	// recipe — keeping the server check identical avoids a class of
	// edge cases where the client sends what looks like a valid tag
	// (e.g. a full-width space `"　"`) and the server silently accepts
	// it with normalized="" (round 2 review feedback / round 6 review
	// feedback).
	tagNormalized := tag.Normalize(req.Tag)
	if tagNormalized == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_tag"})
		return
	}

	// Build the TagInput from the user-entered string. Reusing
	// normalizeTagInputs keeps display-name preservation aligned with
	// existing single-item handlers (Issue #115 / AC 1.3 — chip casing
	// follows user input). The slice we feed in has exactly one
	// element and the helper always returns one TagInput for a
	// non-empty input, so taking element 0 is safe.
	tagInput := normalizeTagInputs([]string{req.Tag})[0]

	succeeded, err := s.bulkStore.BulkAddItemTag(r.Context(), user.ID, validIDs, tagInput)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	succeededIDs := make([]string, 0, len(succeeded))
	for _, s := range succeeded {
		succeededIDs = append(succeededIDs, s.ItemID)
	}
	failed := computeFailedIDs(req.ItemIDs, succeededIDs, invalidIDs)
	failedDetails := buildFailureDetails(failed)
	failedIDsForLog := failedIDsForLog(failedDetails)

	// The `succeeded` slice from store carries store.BulkTagResult
	// values; convert them to BulkTagSuccessDetail (same shape, just
	// the public API name) so the response struct's JSON tags drive
	// the wire format.
	successDetails := make([]BulkTagSuccessDetail, 0, len(succeeded))
	for _, s := range succeeded {
		successDetails = append(successDetails, BulkTagSuccessDetail{
			ItemID: s.ItemID,
			Tags:   s.Tags,
		})
	}

	s.logger.Info("items.bulk.tag",
		slog.String("user_id", user.ID),
		slog.Any("item_ids", req.ItemIDs),
		slog.String("tag_normalized", tagNormalized),
		slog.Int("succeeded_count", len(succeeded)),
		slog.Int("failed_count", len(failedDetails)),
		slog.Any("failed_ids", failedIDsForLog),
		slog.String("request_id", s.requestID(r.Context())))

	writeJSON(w, http.StatusOK, BulkTagResponse{
		Succeeded: successDetails,
		Failed:    failedDetails,
	})
}

// partitionByUUID splits the requested ids into a slice that passes
// uuid.Parse (and is safe to feed to the store layer's pgx-backed
// SQL) and a slice that does not. Original ordering is preserved
// within each output slice so the failed[] response and structured
// log carry the same ids the client supplied (request_id correlation).
func partitionByUUID(ids []string) (valid, invalid []string) {
	valid = make([]string, 0, len(ids))
	invalid = []string{}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			invalid = append(invalid, id)
			continue
		}
		valid = append(valid, id)
	}
	return valid, invalid
}

// computeFailedIDs returns the set of ids the client requested that
// did NOT make it into the store's succeeded set. invalidIDs (caught
// by the per-id UUID check before reaching the store) are merged in
// here so they appear in failed[] with the same reason:"not_found"
// collapse. Original ordering follows the original request slice,
// then any invalid ids that were originally interspersed.
//
// We compute failed = requestIDs \ succeededIDs (using the succeeded
// set for O(N) lookups) so that the client can correlate the failure
// list with the same ids it sent in (Req 4.8 / 5.8 / 8.3).
func computeFailedIDs(requestIDs, succeededIDs, invalidIDs []string) []string {
	succeededSet := make(map[string]struct{}, len(succeededIDs))
	for _, id := range succeededIDs {
		succeededSet[id] = struct{}{}
	}
	invalidSet := make(map[string]struct{}, len(invalidIDs))
	for _, id := range invalidIDs {
		invalidSet[id] = struct{}{}
	}
	failed := make([]string, 0, len(requestIDs))
	for _, id := range requestIDs {
		if _, isInvalid := invalidSet[id]; isInvalid {
			// invalid ids are caught here so we keep the original
			// position in the request rather than appending at the
			// tail; this keeps the failed[] response aligned with
			// the order the client supplied.
			failed = append(failed, id)
			continue
		}
		if _, ok := succeededSet[id]; !ok {
			failed = append(failed, id)
		}
	}
	return failed
}

// buildFailureDetails wraps each id with the canonical
// reason:"not_found" envelope. Reason values other than "not_found"
// are intentionally not produced in v1; ownership / existence /
// invalid-UUID branches all collapse to the same string so the
// response shape cannot leak information about other users' items
// (Security Considerations 節 / Req 4.7 / 8.2 / 8.3).
func buildFailureDetails(failedIDs []string) []BulkFailureDetail {
	out := make([]BulkFailureDetail, 0, len(failedIDs))
	for _, id := range failedIDs {
		out = append(out, BulkFailureDetail{
			ItemID: id,
			Reason: "not_found",
		})
	}
	return out
}

// failedIDsForLog projects the failure detail list to a slice of just
// the ids for the structured slog field. We expose the raw id list
// (not the {item_id, reason} struct) so log analysis tools can
// aggregate on user_id + item_id with simple set operations.
func failedIDsForLog(details []BulkFailureDetail) []string {
	out := make([]string, 0, len(details))
	for _, d := range details {
		out = append(out, d.ItemID)
	}
	return out
}
