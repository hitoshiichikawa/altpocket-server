package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestItemListRowJSONUsesSnakeCase(t *testing.T) {
	row := ItemListRow{
		Item: Item{
			ID:               "item-1",
			UserID:           "user-1",
			URL:              "https://example.com",
			CanonicalURL:     "https://example.com",
			CanonicalHash:    "hash",
			Title:            "title",
			Excerpt:          "excerpt",
			FetchStatus:      "success",
			FetchError:       "",
			CreatedAt:        time.Unix(1700000000, 0).UTC(),
			RefetchRequested: true,
		},
		Tags: []Tag{{ID: "tag-1", Name: "go", NormalizedName: "go"}},
	}

	m := marshalObject(t, row)

	assertHasKey(t, m, "id")
	assertHasKey(t, m, "user_id")
	assertHasKey(t, m, "canonical_url")
	assertHasKey(t, m, "created_at")
	assertHasKey(t, m, "refetch_requested")
	assertHasKey(t, m, "tags")

	assertMissingKey(t, m, "ID")
	assertMissingKey(t, m, "UserID")
	assertMissingKey(t, m, "CanonicalURL")
	assertMissingKey(t, m, "CreatedAt")
	assertMissingKey(t, m, "RefetchRequested")
	assertMissingKey(t, m, "Tags")
}

func TestItemDetailJSONUsesSnakeCase(t *testing.T) {
	detail := ItemDetail{
		Item: Item{
			ID: "item-1",
		},
		ContentFull: "full text",
		Tags:        []Tag{{ID: "tag-1", Name: "go", NormalizedName: "go"}},
	}

	m := marshalObject(t, detail)

	assertHasKey(t, m, "content_full")
	assertHasKey(t, m, "tags")
	assertMissingKey(t, m, "ContentFull")
	assertMissingKey(t, m, "Tags")
}

func TestPaginationJSONUsesSnakeCase(t *testing.T) {
	p := Pagination{Page: 1, PerPage: 30, Total: 100}
	m := marshalObject(t, p)

	assertHasKey(t, m, "page")
	assertHasKey(t, m, "per_page")
	assertHasKey(t, m, "total")

	assertMissingKey(t, m, "Page")
	assertMissingKey(t, m, "PerPage")
	assertMissingKey(t, m, "Total")
}

func TestTagJSONUsesSnakeCase(t *testing.T) {
	tag := Tag{ID: "tag-1", Name: "go", NormalizedName: "go", Count: 2}
	m := marshalObject(t, tag)

	assertHasKey(t, m, "id")
	assertHasKey(t, m, "name")
	assertHasKey(t, m, "normalized_name")
	assertHasKey(t, m, "count")

	assertMissingKey(t, m, "ID")
	assertMissingKey(t, m, "Name")
	assertMissingKey(t, m, "NormalizedName")
	assertMissingKey(t, m, "Count")
}

func marshalObject(t *testing.T, v any) map[string]any {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	return m
}

func assertHasKey(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if _, ok := m[key]; !ok {
		t.Fatalf("expected key %q, got %v", key, m)
	}
}

func assertMissingKey(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if _, ok := m[key]; ok {
		t.Fatalf("did not expect key %q, got %v", key, m)
	}
}
