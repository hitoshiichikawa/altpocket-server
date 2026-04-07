package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"altpocket/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeDataSource captures call arguments and returns canned responses.
type fakeDataSource struct {
	// captured args
	listItemsCalls   int
	listItemsArgs    listItemsCall
	getDetailArgs    getDetailCall
	listTagsArgs     listTagsCall
	listRecentArgs   listRecentCall
	listRecentCalled bool

	// canned returns
	listItems  []store.ItemListRow
	pagination store.Pagination
	listErr    error

	detail    store.ItemDetail
	detailErr error

	tags    []store.Tag
	tagsErr error

	recent    []store.ItemListRow
	recentErr error
}

type listItemsCall struct {
	UserID  string
	Page    int
	PerPage int
	Q       string
	Tags    []string
	Sort    string
}

type getDetailCall struct {
	UserID string
	ItemID string
}

type listTagsCall struct {
	UserID string
	Q      string
	Sel    []string
}

type listRecentCall struct {
	UserID string
	Since  time.Time
}

func (f *fakeDataSource) ListItems(_ context.Context, userID string, page, perPage int, q string, tags []string, sort string) ([]store.ItemListRow, store.Pagination, error) {
	f.listItemsCalls++
	f.listItemsArgs = listItemsCall{userID, page, perPage, q, tags, sort}
	return f.listItems, f.pagination, f.listErr
}

func (f *fakeDataSource) GetItemDetail(_ context.Context, userID, itemID string) (store.ItemDetail, error) {
	f.getDetailArgs = getDetailCall{userID, itemID}
	return f.detail, f.detailErr
}

func (f *fakeDataSource) ListTagsWithCountFiltered(_ context.Context, userID, q string, sel []string) ([]store.Tag, error) {
	f.listTagsArgs = listTagsCall{userID, q, sel}
	return f.tags, f.tagsErr
}

func (f *fakeDataSource) ListRecentItems(_ context.Context, userID string, since time.Time) ([]store.ItemListRow, error) {
	f.listRecentCalled = true
	f.listRecentArgs = listRecentCall{userID, since}
	return f.recent, f.recentErr
}

// resultPayload extracts the JSON-encoded text content from a tool result.
func resultPayload(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil {
		t.Fatal("nil tool result")
	}
	if len(res.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, tc.Text)
	}
	return out
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

// --- list_items ---

func TestListItemsHandler_AppliesDefaults(t *testing.T) {
	ds := &fakeDataSource{
		pagination: store.Pagination{Page: 1, PerPage: 30, Total: 0},
	}
	h := listItemsHandler(ds, "u1")

	res, _, err := h(context.Background(), nil, ListItemsInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError, payload=%s", resultText(t, res))
	}
	if got := ds.listItemsArgs; got.Page != 1 || got.PerPage != 30 || got.Sort != "newest" || got.UserID != "u1" {
		t.Fatalf("defaults not applied: %+v", got)
	}
}

func TestListItemsHandler_ClampsOutOfRange(t *testing.T) {
	ds := &fakeDataSource{}
	h := listItemsHandler(ds, "u1")

	if _, _, err := h(context.Background(), nil, ListItemsInput{Page: -5, PerPage: 999, Sort: "oldest"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := ds.listItemsArgs; got.Page != 1 || got.PerPage != 30 || got.Sort != "oldest" {
		t.Fatalf("expected page=1 perPage=30 sort=oldest, got %+v", got)
	}
}

func TestListItemsHandler_PerPageMaxBoundary(t *testing.T) {
	ds := &fakeDataSource{}
	h := listItemsHandler(ds, "u1")

	if _, _, err := h(context.Background(), nil, ListItemsInput{PerPage: 50}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if ds.listItemsArgs.PerPage != 50 {
		t.Fatalf("perPage 50 should be accepted, got %d", ds.listItemsArgs.PerPage)
	}
}

func TestListItemsHandler_StoreErrorReturnsToolError(t *testing.T) {
	ds := &fakeDataSource{listErr: errors.New("boom")}
	h := listItemsHandler(ds, "u1")

	res, _, err := h(context.Background(), nil, ListItemsInput{})
	if err != nil {
		t.Fatalf("handler should not return Go error, got %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(resultText(t, res), "データベース接続エラー") {
		t.Fatalf("unexpected error text: %q", resultText(t, res))
	}
}

// --- search_items ---

func TestSearchItemsHandler_RequiresQueryOrTags(t *testing.T) {
	ds := &fakeDataSource{}
	h := searchItemsHandler(ds, "u1")

	res, _, err := h(context.Background(), nil, SearchItemsInput{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when both query and tags omitted")
	}
	if ds.listItemsCalls != 0 {
		t.Fatal("store must not be called when args are missing")
	}
}

func TestSearchItemsHandler_QueryUsesRelevanceSort(t *testing.T) {
	ds := &fakeDataSource{}
	h := searchItemsHandler(ds, "u1")

	if _, _, err := h(context.Background(), nil, SearchItemsInput{Query: "go"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if ds.listItemsArgs.Sort != "relevance" {
		t.Fatalf("expected sort=relevance, got %q", ds.listItemsArgs.Sort)
	}
	if ds.listItemsArgs.Q != "go" {
		t.Fatalf("expected q=go, got %q", ds.listItemsArgs.Q)
	}
}

func TestSearchItemsHandler_TagsOnlyUsesNewestSort(t *testing.T) {
	ds := &fakeDataSource{}
	h := searchItemsHandler(ds, "u1")

	if _, _, err := h(context.Background(), nil, SearchItemsInput{Tags: []string{"a", "b"}}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if ds.listItemsArgs.Sort != "newest" {
		t.Fatalf("expected sort=newest, got %q", ds.listItemsArgs.Sort)
	}
	if len(ds.listItemsArgs.Tags) != 2 {
		t.Fatalf("expected 2 tags forwarded, got %v", ds.listItemsArgs.Tags)
	}
}

func TestSearchItemsHandler_ClampsPagination(t *testing.T) {
	ds := &fakeDataSource{}
	h := searchItemsHandler(ds, "u1")

	if _, _, err := h(context.Background(), nil, SearchItemsInput{Query: "x", Page: 0, PerPage: 1000}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if ds.listItemsArgs.Page != 1 || ds.listItemsArgs.PerPage != 30 {
		t.Fatalf("clamping failed: page=%d perPage=%d", ds.listItemsArgs.Page, ds.listItemsArgs.PerPage)
	}
}

// --- get_item ---

func TestGetItemHandler_RequiresID(t *testing.T) {
	ds := &fakeDataSource{}
	h := getItemHandler(ds, "u1")

	res, _, err := h(context.Background(), nil, GetItemInput{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when id is empty")
	}
	if ds.getDetailArgs.ItemID != "" {
		t.Fatal("store must not be called when id is empty")
	}
}

func TestGetItemHandler_NotFound(t *testing.T) {
	ds := &fakeDataSource{detailErr: errors.New("no rows")}
	h := getItemHandler(ds, "u1")

	res, _, err := h(context.Background(), nil, GetItemInput{ID: "abc"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when store returns error")
	}
	if !strings.Contains(resultText(t, res), "記事が見つかりません") {
		t.Fatalf("unexpected text: %q", resultText(t, res))
	}
}

func TestGetItemHandler_PendingHidesContent(t *testing.T) {
	ds := &fakeDataSource{
		detail: store.ItemDetail{
			Item: store.Item{
				ID:          "id1",
				URL:         "https://example.com",
				Title:       "T",
				Excerpt:     "E",
				FetchStatus: "pending",
				CreatedAt:   time.Now(),
			},
			ContentFull: "should-not-leak",
		},
	}
	h := getItemHandler(ds, "u1")

	res, _, _ := h(context.Background(), nil, GetItemInput{ID: "id1"})
	payload := resultPayload(t, res)
	if payload["content_full"] != "" {
		t.Fatalf("pending status must zero out content_full, got %v", payload["content_full"])
	}
	if payload["fetch_status"] != "pending" {
		t.Fatalf("fetch_status not preserved: %v", payload["fetch_status"])
	}
}

func TestGetItemHandler_ReadyReturnsContent(t *testing.T) {
	ds := &fakeDataSource{
		detail: store.ItemDetail{
			Item: store.Item{
				ID:          "id1",
				URL:         "https://example.com",
				Title:       "T",
				FetchStatus: "ready",
				CreatedAt:   time.Now(),
			},
			ContentFull: "the body",
			Tags:        []store.Tag{{Name: "go", NormalizedName: "go"}},
		},
	}
	h := getItemHandler(ds, "u1")

	res, _, _ := h(context.Background(), nil, GetItemInput{ID: "id1"})
	payload := resultPayload(t, res)
	if payload["content_full"] != "the body" {
		t.Fatalf("expected content_full=the body, got %v", payload["content_full"])
	}
	tags, ok := payload["tags"].([]any)
	if !ok || len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %v", payload["tags"])
	}
}

// --- list_tags ---

func TestListTagsHandler_ForwardsQuery(t *testing.T) {
	ds := &fakeDataSource{
		tags: []store.Tag{
			{Name: "Go", NormalizedName: "go", Count: 5},
			{Name: "Rust", NormalizedName: "rust", Count: 2},
		},
	}
	h := listTagsHandler(ds, "u1")

	res, _, err := h(context.Background(), nil, ListTagsInput{Query: "G"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ds.listTagsArgs.Q != "G" {
		t.Fatalf("query not forwarded: %q", ds.listTagsArgs.Q)
	}
	payload := resultPayload(t, res)
	tags, _ := payload["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags in payload, got %d", len(tags))
	}
}

// --- recent_articles resource ---

func TestRecentArticlesHandler_Returns24HourWindow(t *testing.T) {
	ds := &fakeDataSource{
		recent: []store.ItemListRow{
			{Item: store.Item{ID: "a", URL: "https://x", Title: "T", CreatedAt: time.Now()}},
		},
	}
	h := recentArticlesHandler(ds, "u1")

	before := time.Now()
	res, err := h(context.Background(), nil)
	after := time.Now()

	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ds.listRecentCalled {
		t.Fatal("ListRecentItems was not called")
	}
	delta := before.Sub(ds.listRecentArgs.Since)
	if delta < 23*time.Hour+59*time.Minute || delta > 24*time.Hour+1*time.Second {
		t.Fatalf("since must be ~24h before now, delta=%v (before=%v, since=%v)", delta, before, ds.listRecentArgs.Since)
	}
	if after.Sub(ds.listRecentArgs.Since) > 25*time.Hour {
		t.Fatalf("since too far in past")
	}
	if len(res.Contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(res.Contents))
	}
	c := res.Contents[0]
	if c.URI != "altpocket://recent-articles" || c.MIMEType != "application/json" {
		t.Fatalf("wrong URI/MIME: %+v", c)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(c.Text), &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["count"].(float64) != 1 {
		t.Fatalf("expected count=1, got %v", payload["count"])
	}
}

func TestRecentArticlesHandler_EmptyReturnsValidJSON(t *testing.T) {
	ds := &fakeDataSource{recent: nil}
	h := recentArticlesHandler(ds, "u1")

	res, err := h(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload["count"].(float64) != 0 {
		t.Fatalf("expected count=0, got %v", payload["count"])
	}
	if _, ok := payload["articles"].([]any); !ok {
		t.Fatalf("articles must be a JSON array even when empty: %v", payload["articles"])
	}
}

// --- New() smoke test ---

func TestNew_RegistersAllToolsAndResource(t *testing.T) {
	ds := &fakeDataSource{}
	srv := New(ds, "u1")
	if srv == nil {
		t.Fatal("New returned nil")
	}
}
