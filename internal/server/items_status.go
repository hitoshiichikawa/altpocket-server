// Package server: items.status (user-visible lifecycle state) wiring for
// Issue #119. This file holds the HTTP / parser helpers that translate
// between the URL / JSON layer and the store.UpdateItemStatus /
// store.ListItems(... statuses ...) primitives added in task 2.
//
// The handler and helpers here intentionally live in their own file so the
// existing server.go can stay focused on the legacy item endpoints. Routes
// and the handleListItems / handleUIItems wiring still live in server.go;
// only the new behaviour-changing surfaces are here.
package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"altpocket/internal/auth"
	"altpocket/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Canonical `?status=` query values understood by the Web UI tab parser
// (Req 3.3 / 3.4 / 3.5). They are the user-visible identifiers that drive
// the URL and the active tab marker; the store layer still accepts the
// 3-value enum ItemStatusUnread / ItemStatusRead / ItemStatusArchived as
// the actual filter values (see parseStatusFilter's mapping below).
const (
	statusTabUnread   = "unread"
	statusTabAll      = "all"
	statusTabArchived = "archived"
)

// parseStatusFilter converts the `?status=` query parameter into the
// statuses slice consumed by store.ListItems. The caller passes the
// default value used when the query is absent / empty / unknown, so
// /ui/items (Web UI) and /v1/items (REST API) can apply different defaults
// to the same parser:
//
//   - handleUIItems (Web UI):   defaultIfEmpty = []string{ItemStatusUnread}
//     so the Unread tab is the entry point (Req 3.1).
//   - handleListItems (REST):   defaultIfEmpty = nil
//     so existing extension / external API clients that do not send
//     `?status=` continue to receive all states (Req 6.2 backward compat).
//
// Canonical 3-value mapping (after applying defaultIfEmpty for "" inputs):
//
//	"unread"   → []string{ItemStatusUnread}            (Req 3.3)
//	"all"      → []string{ItemStatusUnread, ItemStatusRead}
//	                                                   (Req 3.4 / 設計確認事項 (d): archived 除外)
//	"archived" → []string{ItemStatusArchived}          (Req 3.5)
//	""         → defaultIfEmpty
//	others     → defaultIfEmpty (including "read" alone — the UI tabs are
//	             Unread / All / Archived only, so accepting `?status=read`
//	             as a single-state filter would expose a 4th tab not in
//	             the spec and contradict Req 3.2 / 3.7).
//
// The match is case-insensitive on the input value so URLs typed by hand
// (`?status=All`) still resolve. The MCP-side equivalent mcpStatusFilter
// (task 5) is independent and does accept "read" as a single value
// because Tool input is an explicit API parameter rather than a UI tab.
func parseStatusFilter(q url.Values, defaultIfEmpty []string) []string {
	raw := strings.ToLower(strings.TrimSpace(q.Get("status")))
	switch raw {
	case statusTabUnread:
		return []string{store.ItemStatusUnread}
	case statusTabAll:
		return []string{store.ItemStatusUnread, store.ItemStatusRead}
	case statusTabArchived:
		return []string{store.ItemStatusArchived}
	default:
		return defaultIfEmpty
	}
}

// resolveStatusTab returns the canonical tab identifier ("unread" / "all"
// / "archived") that the Web UI should render as active for the given
// raw `?status=` query value. Unknown / empty values resolve to the
// default Unread tab so the SSR markup stays in sync with handleUIItems'
// defaultIfEmpty (Req 3.1 / 3.8).
func resolveStatusTab(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case statusTabAll:
		return statusTabAll
	case statusTabArchived:
		return statusTabArchived
	default:
		// "unread", "" and any unknown / case-variant input all collapse
		// to the Unread default so the tab highlight matches the parsed
		// statuses slice.
		return statusTabUnread
	}
}

// buildStatusTabURLs returns the navigation URL for each status tab,
// preserving every other query parameter (q / tag / sort / per_page /
// page). The "Unread" tab uses the canonical `?status=unread` form
// (rather than dropping the parameter) so URL sharing behaves the same
// way regardless of which tab is selected — clicking Unread on a shared
// `?status=archived` link still produces a URL that explicitly pins the
// tab (Req 3.6 / 3.8). `page` is intentionally NOT reset here; pagination
// reset semantics on tab switch are owned by the JS layer in task 9
// (`static/items_status.js`), so the SSR fallback keeps the current page
// and the user can navigate back via Prev/Next links.
func buildStatusTabURLs(currentURL *url.URL) map[string]string {
	return map[string]string{
		statusTabUnread:   buildStatusTabURL(currentURL, statusTabUnread),
		statusTabAll:      buildStatusTabURL(currentURL, statusTabAll),
		statusTabArchived: buildStatusTabURL(currentURL, statusTabArchived),
	}
}

// buildStatusTabURL returns a URL with `?status=<value>` set, preserving
// the rest of the query. Used both by buildStatusTabURLs (SSR tab anchors)
// and by JS callers via the SSR-rendered hrefs (task 9).
func buildStatusTabURL(currentURL *url.URL, statusValue string) string {
	u := cloneURL(currentURL)
	q := u.Query()
	q.Set("status", statusValue)
	u.RawQuery = q.Encode()
	return u.String()
}

// buildLibraryURL returns the `?status=`-preserving URL used by the
// item-detail page's "Library" back link (Req 3.8 / Reviewer 指摘 #2).
//
// Issue #119 keeps the active status tab in the URL while the user is
// browsing the library. When the user opens a detail page from
// `/ui/items?status=archived` and then clicks the "← Library" link, we
// want to land back on the Archived tab, not snap to the default
// Unread tab.
//
// The raw `?status=` value (the user-visible canonical tab name, or
// any case-variant they typed) is preserved verbatim — even a
// non-canonical value like `?status=Read` survives, because
// parseStatusFilter will collapse the unknown value to the
// defaultIfEmpty fallback on the library side, which is semantically
// harmless.
//
// Inputs:
//   - rawStatus: r.URL.Query().Get("status") from /ui/items/{id}, i.e.
//     the carry-over from the detail page URL.
//
// Outputs:
//   - "" or whitespace → "/ui/items"  (Req 3.1 default Unread).
//   - any other value  → "/ui/items?status=<value>" with URL-safe
//     escaping (url.Values.Encode).
//
// Only `?status=` is propagated. The detail page never gets `?q=` or
// `?tag=` in its URL (it is keyed by `{id}`), so propagating other
// query parameters is unnecessary and would just clutter the back
// link. This is intentionally narrower than buildClearFiltersURL,
// which has to preserve a full filter context.
func buildLibraryURL(rawStatus string) string {
	const libraryPath = "/ui/items"
	if strings.TrimSpace(rawStatus) == "" {
		return libraryPath
	}
	q := url.Values{"status": {rawStatus}}
	return libraryPath + "?" + q.Encode()
}

// buildClearFiltersURL returns a URL with every filter parameter
// (q / tag / tags) removed, **preserving** the status tab so a Clear
// Filters click does not silently snap the user back from Archived / All
// to Unread (Req 3.6 / 3.8). Other navigation context (sort / per_page)
// is dropped along with the filters so the user lands on a fresh
// listing view; only the active status tab and page parameter are kept.
func buildClearFiltersURL(currentURL *url.URL) string {
	u := cloneURL(currentURL)
	q := u.Query()
	statusValue := strings.TrimSpace(q.Get("status"))
	// Drop every search / tag filter parameter.
	q.Del("q")
	q.Del("tag")
	q.Del("tags")
	// Reset pagination so Clear Filters lands on the first page.
	q.Del("page")
	if statusValue != "" {
		q.Set("status", statusValue)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// setItemStatusRequest is the JSON body schema accepted by
// PATCH /v1/items/{id}/status. The struct only carries the next status
// value; the item id is taken from the URL path (chi.URLParam) and the
// user is taken from the authenticated context (auth.UserFromContext).
type setItemStatusRequest struct {
	Status string `json:"status"`
}

// handleSetItemStatus transitions a single item's user-visible status to
// the value supplied in the request body. The path-level URL parameter
// {id} identifies the item and the requesting user is taken from
// requireAuth's auth.UserFromContext context.
//
// Request:  PATCH /v1/items/{id}/status   {"status":"unread"|"read"|"archived"}
//
// Responses:
//   - 200 OK    {"status":"<next>","item_id":"<id>"}
//   - 400       {"error":"invalid_request"}  — body unparseable / status missing
//   - 400       {"error":"invalid_status"}   — status not in the canonical enum
//   - 401       {"error":"unauthorized"}     — via requireAuth (defense; the
//     middleware short-circuits earlier so the body
//     path here is only reached for the auth user)
//   - 404       {"error":"not_found"}        — item not owned by this user OR
//     does not exist; both branches are collapsed
//     into the same response to avoid leaking
//     ownership information (NFR 2.1).
//   - 429       {"error":"rate_limited"}     — per-user limiter
//   - 500       {"error":"db_error"}         — any other store error
//
// On success the handler emits a structured `items.status.update`
// slog.Info line with user_id / item_id / prev / next / request_id but
// never includes the session cookie, Authorization header, or request
// body raw value (NFR 3.1).
func (s *Server) handleSetItemStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !s.limiter.Allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}

	var req setItemStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	next := strings.TrimSpace(req.Status)
	if next == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if !isCanonicalItemStatus(next) {
		// enum violation — distinct from the missing/empty case so MCP /
		// extension clients can tell the two failure modes apart
		// (Req 1.5: enum range rejection).
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_status"})
		return
	}

	itemID := chi.URLParam(r, "id")
	prev, err := s.store.UpdateItemStatus(r.Context(), user.ID, itemID, next)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Item does not exist OR is owned by another user. We collapse
			// the two branches into the same not_found response so the
			// caller cannot infer ownership of an unrelated id (NFR 2.1).
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	// Structured transition log (NFR 3.1). The 4 named fields
	// (user_id / item_id / prev / next) plus request_id are sufficient
	// to reconstruct the timeline; we intentionally never log the
	// session cookie, Authorization header, refresh token or the raw
	// JSON body value.
	s.logger.Info("items.status.update",
		slog.String("user_id", user.ID),
		slog.String("item_id", itemID),
		slog.String("prev", prev),
		slog.String("next", next),
		slog.String("request_id", s.requestID(r.Context())))

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  next,
		"item_id": itemID,
	})
}

// isCanonicalItemStatus reports whether v is one of the three canonical
// items.status enum values. Defense-in-depth: the database also enforces
// this via the items_status_check CHECK constraint added in
// migrations/007, but rejecting at the API layer gives clients a clean
// 400 invalid_status response instead of a 500 db_error (Req 1.5).
func isCanonicalItemStatus(v string) bool {
	switch v {
	case store.ItemStatusUnread, store.ItemStatusRead, store.ItemStatusArchived:
		return true
	default:
		return false
	}
}
