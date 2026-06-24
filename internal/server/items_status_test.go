package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"altpocket/internal/auth"
	"altpocket/internal/store"
)

// Test_parseStatusFilter_TableDriven exercises the canonical mapping
// described in the items_status.go doc-comment for every documented
// input shape, against both `/v1/items` (defaultIfEmpty=nil) and
// `/ui/items` (defaultIfEmpty=[]string{"unread"}) call sites.
//
// Covers Req 3.1 (Unread default for UI), 3.3 (unread→[unread]),
// 3.4 (all→[unread,read]), 3.5 (archived→[archived]) and Req 6.2
// (REST default = nil = whole-set / backward compatibility). The
// `read` single-value case is explicitly pinned to defaultIfEmpty —
// the UI tabs are Unread / All / Archived only and accepting
// `?status=read` standalone would expose a non-spec 4th tab.
func Test_parseStatusFilter_TableDriven(t *testing.T) {
	defaultsUI := []string{store.ItemStatusUnread}
	defaultsREST := []string(nil)

	cases := []struct {
		name        string
		queryValue  string
		wantUI      []string
		wantREST    []string
		description string
	}{
		{
			name:        "absent uses defaults",
			queryValue:  "__omit__",
			wantUI:      defaultsUI,
			wantREST:    defaultsREST,
			description: "/ui/items defaults to Unread (Req 3.1), /v1/items defaults to nil (Req 6.2)",
		},
		{
			name:        "empty value uses defaults",
			queryValue:  "",
			wantUI:      defaultsUI,
			wantREST:    defaultsREST,
			description: "explicit ?status= with no value collapses into the same fallback as missing",
		},
		{
			name:        "unread tab maps to [unread]",
			queryValue:  "unread",
			wantUI:      []string{store.ItemStatusUnread},
			wantREST:    []string{store.ItemStatusUnread},
			description: "Req 3.3",
		},
		{
			name:        "all tab maps to [unread, read]",
			queryValue:  "all",
			wantUI:      []string{store.ItemStatusUnread, store.ItemStatusRead},
			wantREST:    []string{store.ItemStatusUnread, store.ItemStatusRead},
			description: "Req 3.4 + 設計確認事項 (d): archived は除外",
		},
		{
			name:        "archived tab maps to [archived]",
			queryValue:  "archived",
			wantUI:      []string{store.ItemStatusArchived},
			wantREST:    []string{store.ItemStatusArchived},
			description: "Req 3.5",
		},
		{
			name:        "read alone falls back to defaults (no UI tab)",
			queryValue:  "read",
			wantUI:      defaultsUI,
			wantREST:    defaultsREST,
			description: "UI tabs are Unread/All/Archived only — accepting `read` would expose a non-spec 4th tab (Req 3.2 / 3.7)",
		},
		{
			name:        "unknown value falls back to defaults",
			queryValue:  "draft",
			wantUI:      defaultsUI,
			wantREST:    defaultsREST,
			description: "unknown values must fall back to defaults (Req 6.2 robustness)",
		},
		{
			name:        "mixed case unread is accepted",
			queryValue:  "Unread",
			wantUI:      []string{store.ItemStatusUnread},
			wantREST:    []string{store.ItemStatusUnread},
			description: "case-insensitive matching for hand-typed URLs",
		},
		{
			name:        "mixed case archived is accepted",
			queryValue:  "ARCHIVED",
			wantUI:      []string{store.ItemStatusArchived},
			wantREST:    []string{store.ItemStatusArchived},
			description: "case-insensitive matching for hand-typed URLs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/ui", func(t *testing.T) {
			q := url.Values{}
			if tc.queryValue != "__omit__" {
				q.Set("status", tc.queryValue)
			}
			got := parseStatusFilter(q, defaultsUI)
			if !reflect.DeepEqual(got, tc.wantUI) {
				t.Fatalf("parseStatusFilter(?status=%q, ui-default) = %#v, want %#v (%s)",
					tc.queryValue, got, tc.wantUI, tc.description)
			}
		})
		t.Run(tc.name+"/rest", func(t *testing.T) {
			q := url.Values{}
			if tc.queryValue != "__omit__" {
				q.Set("status", tc.queryValue)
			}
			got := parseStatusFilter(q, defaultsREST)
			if !reflect.DeepEqual(got, tc.wantREST) {
				t.Fatalf("parseStatusFilter(?status=%q, rest-default) = %#v, want %#v (%s)",
					tc.queryValue, got, tc.wantREST, tc.description)
			}
		})
	}
}

// TestHandleSetItemStatusUnauthorizedReturnsJSONError pins the JSON
// envelope contract for missing auth context. The route is wired
// behind requireAuth in Routes(), but direct invocation goes through
// the same auth.UserFromContext check so the handler still returns the
// canonical {"error":"unauthorized"} body documented in design.md
// (rather than HTTP plaintext).
func TestHandleSetItemStatusUnauthorizedReturnsJSONError(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/v1/items/item-1/status",
		strings.NewReader(`{"status":"read"}`))
	rr := httptest.NewRecorder()

	s.handleSetItemStatus(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %q", rr.Body.String())
	}
}

// TestHandleSetItemStatusInvalidJSONReturns400 pins the contract that
// a syntactically invalid JSON body returns 400 invalid_request rather
// than 500. Matches handlePatchItem's existing behavior for parse
// failures.
func TestHandleSetItemStatusInvalidJSONReturns400(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/v1/items/item-1/status",
		strings.NewReader(`{not-json`))
	req = req.WithContext(auth.ContextWithUser(req.Context(),
		auth.User{ID: "user-1"}))
	rr := httptest.NewRecorder()

	s.handleSetItemStatus(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}

// TestHandleSetItemStatusEmptyStatusReturns400 covers two missing-value
// shapes — {} (no key at all) and {"status":""} (key present but empty)
// — both treated as invalid_request per design.md. The handler must
// reject before reaching UpdateItemStatus so a parse-good-but-meaning-
// empty body cannot fall into the unknown-status branch (which would
// return invalid_status and confuse clients about whether they sent a
// known typo or simply forgot the value).
func TestHandleSetItemStatusEmptyStatusReturns400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "empty object", body: `{}`},
		{name: "empty status string", body: `{"status":""}`},
		{name: "whitespace-only status string", body: `{"status":"   "}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthTestServer()
			req := httptest.NewRequest(http.MethodPatch, "/v1/items/item-1/status",
				strings.NewReader(tc.body))
			req = req.WithContext(auth.ContextWithUser(req.Context(),
				auth.User{ID: "user-1"}))
			rr := httptest.NewRecorder()

			s.handleSetItemStatus(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "invalid_request") {
				t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "invalid_status") {
				t.Fatalf("empty/missing status must NOT be reported as invalid_status, got %q",
					rr.Body.String())
			}
		})
	}
}

// TestHandleSetItemStatusInvalidStatusReturns400 covers Req 1.5 — out-
// of-enum values must be rejected at the API boundary with a distinct
// invalid_status error code (not invalid_request, not db_error). The
// DB CHECK constraint is defense-in-depth, but rejecting at the API
// layer is the user-facing contract.
func TestHandleSetItemStatusInvalidStatusReturns400(t *testing.T) {
	cases := []string{
		`{"status":"foo"}`,
		`{"status":"bar"}`,
		`{"status":"deleted"}`,
		// case-mismatch values are also out-of-enum: the handler is
		// strict (no lowercasing) because the API contract documents
		// the canonical lower-case enum.
		`{"status":"UNREAD"}`,
		`{"status":"Read"}`,
	}

	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			s := newAuthTestServer()
			req := httptest.NewRequest(http.MethodPatch, "/v1/items/item-1/status",
				strings.NewReader(body))
			req = req.WithContext(auth.ContextWithUser(req.Context(),
				auth.User{ID: "user-1"}))
			rr := httptest.NewRecorder()

			s.handleSetItemStatus(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body=%q)", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "invalid_status") {
				t.Fatalf("expected invalid_status body, got %q", rr.Body.String())
			}
		})
	}
}

// TestHandleUIItemsTemplateDataIncludesStatusQuery exercises the
// URL-builder helpers that the /ui/items handler injects into its
// template data (StatusQuery / StatusTabURLs / StatusTab) plus the
// generic URL builders that must preserve `?status=` for the
// pagination, sort, per-page and clear-filters flows (Req 3.6 / 3.8).
//
// Because the handler itself requires a full *Renderer + DB, the test
// asserts the building blocks directly — that is the source of truth
// for the data path the handler will assemble.
func TestHandleUIItemsTemplateDataIncludesStatusQuery(t *testing.T) {
	t.Run("resolveStatusTab maps the canonical 3 values", func(t *testing.T) {
		cases := map[string]string{
			"":         statusTabUnread, // default
			"unread":   statusTabUnread,
			"all":      statusTabAll,
			"archived": statusTabArchived,
			"read":     statusTabUnread, // not a UI tab; collapses to default
			"unknown":  statusTabUnread, // unknown collapses to default
			"Archived": statusTabArchived,
		}
		for raw, want := range cases {
			if got := resolveStatusTab(raw); got != want {
				t.Errorf("resolveStatusTab(%q) = %q, want %q", raw, got, want)
			}
		}
	})

	t.Run("buildStatusTabURLs preserves existing query parameters", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?q=hello&tag=go&tag=rust&sort=relevance&per_page=20&page=2&status=archived")
		urls := buildStatusTabURLs(u)
		for _, tab := range []string{statusTabUnread, statusTabAll, statusTabArchived} {
			parsed, err := url.Parse(urls[tab])
			if err != nil {
				t.Fatalf("buildStatusTabURLs[%q] is not a valid URL: %v", tab, err)
			}
			if got := parsed.Query().Get("status"); got != tab {
				t.Errorf("tab %q URL has status=%q, want %q", tab, got, tab)
			}
			if got := parsed.Query().Get("q"); got != "hello" {
				t.Errorf("tab %q URL dropped q=hello, got %q", tab, got)
			}
			if got := parsed.Query()["tag"]; len(got) != 2 || got[0] != "go" || got[1] != "rust" {
				t.Errorf("tab %q URL dropped tags, got %#v", tab, got)
			}
			if got := parsed.Query().Get("sort"); got != "relevance" {
				t.Errorf("tab %q URL dropped sort=relevance, got %q", tab, got)
			}
			if got := parsed.Query().Get("per_page"); got != "20" {
				t.Errorf("tab %q URL dropped per_page=20, got %q", tab, got)
			}
			if got := parsed.Query().Get("page"); got != "2" {
				t.Errorf("tab %q URL dropped page=2, got %q", tab, got)
			}
		}
	})

	t.Run("pageURL preserves status across pagination (Req 3.6 / 3.8)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?status=archived&q=hello&sort=newest")
		got, err := url.Parse(pageURL(u, 3))
		if err != nil {
			t.Fatalf("pageURL produced invalid URL: %v", err)
		}
		if v := got.Query().Get("status"); v != "archived" {
			t.Errorf("pageURL dropped status, got %q", v)
		}
		if v := got.Query().Get("page"); v != "3" {
			t.Errorf("pageURL did not set page=3, got %q", v)
		}
		if v := got.Query().Get("q"); v != "hello" {
			t.Errorf("pageURL dropped q, got %q", v)
		}
	})

	t.Run("buildClearAllTagsURL preserves status (Req 3.6 / 3.8)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?status=all&tag=go&tag=rust&q=hello")
		got, _ := url.Parse(buildClearAllTagsURL(u))
		if v := got.Query().Get("status"); v != "all" {
			t.Errorf("buildClearAllTagsURL dropped status=all, got %q", v)
		}
		// And it still removes tag/tags per its own contract.
		if v := got.Query()["tag"]; len(v) != 0 {
			t.Errorf("buildClearAllTagsURL should remove tag, got %#v", v)
		}
	})

	t.Run("buildClearFiltersURL keeps status but drops q / tag / page (Req 3.6 / 3.8)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?status=archived&q=hello&tag=go&tag=rust&tags=news,web&page=3&sort=newest")
		got, err := url.Parse(buildClearFiltersURL(u))
		if err != nil {
			t.Fatalf("buildClearFiltersURL produced invalid URL: %v", err)
		}
		if v := got.Query().Get("status"); v != "archived" {
			t.Errorf("buildClearFiltersURL must keep status=archived, got %q", v)
		}
		if v := got.Query().Get("q"); v != "" {
			t.Errorf("buildClearFiltersURL must drop q, got %q", v)
		}
		if v := got.Query()["tag"]; len(v) != 0 {
			t.Errorf("buildClearFiltersURL must drop tag, got %#v", v)
		}
		if v := got.Query()["tags"]; len(v) != 0 {
			t.Errorf("buildClearFiltersURL must drop tags, got %#v", v)
		}
		if v := got.Query().Get("page"); v != "" {
			t.Errorf("buildClearFiltersURL must reset pagination, got page=%q", v)
		}
	})

	t.Run("buildClearFiltersURL with no status leaves the parameter absent", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?q=hello&tag=go")
		got, _ := url.Parse(buildClearFiltersURL(u))
		if v := got.Query()["status"]; len(v) != 0 {
			t.Errorf("buildClearFiltersURL must not invent a status= parameter, got %#v", v)
		}
	})

	t.Run("buildStatusTabURLs round-trips through parseStatusFilter", func(t *testing.T) {
		// The tab URL produced for "all" must, when parsed back, yield
		// the [unread, read] slice — and likewise for the other tabs.
		// This pins the contract between the SSR template hrefs and
		// the handler-side parser.
		u, _ := url.Parse("/ui/items")
		urls := buildStatusTabURLs(u)
		for _, tab := range []string{statusTabUnread, statusTabAll, statusTabArchived} {
			parsed, _ := url.Parse(urls[tab])
			got := parseStatusFilter(parsed.Query(), nil)
			want := parseStatusFilter(url.Values{"status": {tab}}, nil)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("tab %q URL round-trips to %#v, want %#v", tab, got, want)
			}
		}
	})
}

// TestHandleSetItemStatusReturnsJSONContentType pins the response
// content-type for the validation error envelopes (the success path
// is exercised in the integration-tagged tests). Helps callers that
// branch on Content-Type before parsing.
func TestHandleSetItemStatusReturnsJSONContentType(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/v1/items/item-1/status",
		strings.NewReader(`{"status":"foo"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(),
		auth.User{ID: "user-1"}))
	rr := httptest.NewRecorder()

	s.handleSetItemStatus(rr, req)

	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected JSON content type, got %q", ct)
	}
	// And the body must be parseable JSON.
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v (body=%q)", err, rr.Body.String())
	}
	if body["error"] != "invalid_status" {
		t.Fatalf("expected error=invalid_status, got %#v", body)
	}
}
