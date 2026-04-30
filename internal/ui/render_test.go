package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPageTitleFormat(t *testing.T) {
	r, err := New("../../templates")
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	tests := []struct {
		name     string
		template string
		data     map[string]interface{}
		want     string
	}{
		{
			"home shows login title",
			"home",
			map[string]interface{}{"Title": "ログイン"},
			"<title>ログイン | altpocket</title>",
		},
		{
			"register shows registration title",
			"register",
			map[string]interface{}{"Title": "アカウント登録"},
			"<title>アカウント登録 | altpocket</title>",
		},
		{
			"items shows article list title",
			"items",
			map[string]interface{}{
				"Title":      "記事一覧",
				"Page":       1,
				"TotalPages": 1,
				"PerPage":    30,
				"Sort":       "newest",
			},
			"<title>記事一覧 | altpocket</title>",
		},
		{
			"quick_add shows quick add title",
			"quick_add",
			map[string]interface{}{"Title": "クイック追加"},
			"<title>クイック追加 | altpocket</title>",
		},
		{
			"settings shows settings title",
			"settings",
			map[string]interface{}{"Title": "設定"},
			"<title>設定 | altpocket</title>",
		},
		{
			"detail shows article title",
			"detail",
			map[string]interface{}{"Title": "テスト記事タイトル"},
			"<title>テスト記事タイトル | altpocket</title>",
		},
		{
			"detail shows untitled fallback",
			"detail",
			map[string]interface{}{"Title": "(無題)"},
			"<title>(無題) | altpocket</title>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			if err := r.Render(rr, tc.template, tc.data); err != nil {
				t.Fatalf("render error: %v", err)
			}
			body := rr.Body.String()
			if !strings.Contains(body, tc.want) {
				// Show first 500 chars for debugging
				end := len(body)
				if end > 500 {
					end = 500
				}
				t.Errorf("expected %q in response body, got:\n%s", tc.want, body[:end])
			}
		})
	}
}

type testItemTag struct {
	Name           string
	NormalizedName string
}

type testItemRow struct {
	ID          string
	URL         string
	Title       string
	Excerpt     string
	FetchStatus string
	CreatedAt   time.Time
	Tags        []testItemTag
}

func renderItemsWith(t *testing.T, items []testItemRow) string {
	t.Helper()
	r, err := New("../../templates")
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}
	data := map[string]interface{}{
		"Title":          "記事一覧",
		"Items":          items,
		"Tags":           []testItemTag{},
		"SelectedTags":   map[string]bool{},
		"Page":           1,
		"PerPage":        30,
		"Total":          len(items),
		"TotalPages":     1,
		"Query":          "",
		"Sort":           "newest",
		"PerPageOptions": []int{10, 20, 30, 40, 50},
		"PrevURL":        "",
		"NextURL":        "",
	}
	rr := httptest.NewRecorder()
	if err := r.Render(rr, "items", data); err != nil {
		t.Fatalf("render error: %v", err)
	}
	return rr.Body.String()
}

func TestItemsTagsDivRendering(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	t.Run("タグ0件のカードで .tags div が描画されない", func(t *testing.T) {
		body := renderItemsWith(t, []testItemRow{{
			ID:          "item-empty",
			URL:         "https://example.com/a",
			Title:       "タグなし記事",
			Excerpt:     "本文抜粋",
			FetchStatus: "completed",
			CreatedAt:   now,
			Tags:        nil,
		}})
		// カード自体は描画されている
		if !strings.Contains(body, `id="item-title-item-empty"`) {
			t.Fatalf("expected card for item-empty to be rendered")
		}
		// .tags コンテナは存在しない（サイドバーの .tag-list は別クラスなので衝突しない）
		if strings.Contains(body, `<div class="tags">`) {
			t.Errorf("expected no <div class=\"tags\"> when item has no tags, got body containing it")
		}
	})

	t.Run("タグ複数件のカードで .tags と .tag が描画される", func(t *testing.T) {
		body := renderItemsWith(t, []testItemRow{{
			ID:          "item-tagged",
			URL:         "https://example.com/b",
			Title:       "タグあり記事",
			Excerpt:     "本文抜粋",
			FetchStatus: "completed",
			CreatedAt:   now,
			Tags: []testItemTag{
				{Name: "Go", NormalizedName: "go"},
				{Name: "HTML", NormalizedName: "html"},
			},
		}})
		if !strings.Contains(body, `<div class="tags">`) {
			t.Errorf("expected <div class=\"tags\"> to be rendered when item has tags")
		}
		if c := strings.Count(body, `<span class="tag">`); c != 2 {
			t.Errorf("expected 2 <span class=\"tag\"> elements, got %d", c)
		}
		if !strings.Contains(body, "Go") || !strings.Contains(body, "HTML") {
			t.Errorf("expected tag names to be present in body")
		}
	})

	t.Run("タグ0件とタグありが混在するカードで条件分岐が正しく動く", func(t *testing.T) {
		body := renderItemsWith(t, []testItemRow{
			{
				ID:          "item-empty",
				URL:         "https://example.com/a",
				Title:       "タグなし記事",
				Excerpt:     "e1",
				FetchStatus: "completed",
				CreatedAt:   now,
				Tags:        nil,
			},
			{
				ID:          "item-tagged",
				URL:         "https://example.com/b",
				Title:       "タグあり記事",
				Excerpt:     "e2",
				FetchStatus: "completed",
				CreatedAt:   now,
				Tags:        []testItemTag{{Name: "Go", NormalizedName: "go"}},
			},
		})
		if c := strings.Count(body, `<div class="tags">`); c != 1 {
			t.Errorf("expected exactly 1 <div class=\"tags\">, got %d", c)
		}
		if c := strings.Count(body, `<span class="tag">`); c != 1 {
			t.Errorf("expected exactly 1 <span class=\"tag\">, got %d", c)
		}
	})
}
