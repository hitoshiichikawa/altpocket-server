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

// TestRenderFragmentItemsList verifies that the standalone items_list
// fragment renders only the items region (no layout / no <html>).
//
// This is the rendering path used by the search debounce / URL sync flow
// (Issue #114) to refresh the list without a full page reload.
func TestRenderFragmentItemsList(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	t.Run("fragment contains items but no layout chrome", func(t *testing.T) {
		r, err := New("../../templates")
		if err != nil {
			t.Fatalf("failed to create renderer: %v", err)
		}
		data := map[string]interface{}{
			"Items": []testItemRow{{
				ID:          "item-frag",
				URL:         "https://example.com/a",
				Title:       "Fragment記事",
				Excerpt:     "本文抜粋",
				FetchStatus: "completed",
				CreatedAt:   now,
				Tags:        nil,
			}},
			"Page":       1,
			"TotalPages": 1,
			"PrevURL":    "",
			"NextURL":    "",
		}

		rr := httptest.NewRecorder()
		if err := r.RenderFragment(rr, "items_list", data); err != nil {
			t.Fatalf("RenderFragment error: %v", err)
		}
		body := rr.Body.String()

		if strings.Contains(body, "<!DOCTYPE html>") {
			t.Errorf("fragment must not include doctype, got body containing it")
		}
		if strings.Contains(body, "<html") || strings.Contains(body, "</html>") {
			t.Errorf("fragment must not include <html> root element")
		}
		if strings.Contains(body, "<title>") {
			t.Errorf("fragment must not include <title> element")
		}
		if !strings.Contains(body, `id="item-title-item-frag"`) {
			t.Errorf("expected fragment to contain item card, got:\n%s", body)
		}
		if !strings.Contains(body, `class="pagination"`) {
			t.Errorf("expected fragment to contain pagination block, got:\n%s", body)
		}
	})

	t.Run("empty Items renders empty-state card without layout", func(t *testing.T) {
		r, err := New("../../templates")
		if err != nil {
			t.Fatalf("failed to create renderer: %v", err)
		}
		data := map[string]interface{}{
			"Items":      []testItemRow{},
			"Page":       1,
			"TotalPages": 1,
		}

		rr := httptest.NewRecorder()
		if err := r.RenderFragment(rr, "items_list", data); err != nil {
			t.Fatalf("RenderFragment error: %v", err)
		}
		body := rr.Body.String()

		if !strings.Contains(body, "No articles yet") {
			t.Errorf("expected empty-state card, got:\n%s", body)
		}
		if strings.Contains(body, "<html") {
			t.Errorf("fragment must not include <html> root element")
		}
	})

	t.Run("unknown fragment name returns 500", func(t *testing.T) {
		r, err := New("../../templates")
		if err != nil {
			t.Fatalf("failed to create renderer: %v", err)
		}
		rr := httptest.NewRecorder()
		_ = r.RenderFragment(rr, "does_not_exist", nil)
		if rr.Code != 500 {
			t.Errorf("expected 500 for unknown fragment, got %d", rr.Code)
		}
	})
}

// TestItemsPageEmbedsFragment ensures the full /ui/items page render still
// includes the items list (which now comes from the items_list partial).
// This guards against regressions in the partial wiring.
func TestItemsPageEmbedsFragment(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	body := renderItemsWith(t, []testItemRow{{
		ID:          "item-embed",
		URL:         "https://example.com/c",
		Title:       "埋め込み記事",
		Excerpt:     "本文抜粋",
		FetchStatus: "completed",
		CreatedAt:   now,
	}})

	if !strings.Contains(body, `id="item-title-item-embed"`) {
		t.Errorf("full page render must still include items_list cards")
	}
	if !strings.Contains(body, `id="items-list"`) {
		t.Errorf("full page render must include #items-list region")
	}
	if !strings.Contains(body, `data-items-region`) {
		t.Errorf("full page render must mark #items-list with data-items-region")
	}
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

	t.Run("タグ複数件のカードで .tags と .tag-filter-toggle ボタンが描画される (Issue #117)", func(t *testing.T) {
		// Issue #117: タグは <span> から <button class="tag tag-filter-toggle"> に
		// 変更され、data-tag-normalized / aria-pressed / data-tag-filter-toggle が
		// 付与される。これらは JS (static/items_tags.js) がクリック絞り込みのため
		// に依拠する契約なので、テンプレート側で必ず描画されること。
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
		// 2 件のタグそれぞれに対応する button が描画される。
		if c := strings.Count(body, `data-tag-filter-toggle`); c != 2 {
			t.Errorf("expected 2 data-tag-filter-toggle markers, got %d\nbody:\n%s", c, body)
		}
		if !strings.Contains(body, `data-tag-normalized="go"`) {
			t.Errorf("expected data-tag-normalized=\"go\" attribute, got body:\n%s", body)
		}
		if !strings.Contains(body, `data-tag-normalized="html"`) {
			t.Errorf("expected data-tag-normalized=\"html\" attribute, got body:\n%s", body)
		}
		// <span class="tag"> は廃止: 後方互換を意図して残しているわけではないため、
		// この方向で書かれていないことを念のため確認する（混在防止）。
		if strings.Contains(body, `<span class="tag">`) {
			t.Errorf("legacy <span class=\"tag\"> must not be rendered (Issue #117): %s", body)
		}
		// タグ表示名は人間可読として残る。
		if !strings.Contains(body, "Go") || !strings.Contains(body, "HTML") {
			t.Errorf("expected tag display names to be present in body")
		}
		// type="button" であること（JS 無効環境でも form を勝手に submit しない / NFR 2.1）。
		if !strings.Contains(body, `type="button"`) {
			t.Errorf("expected tag toggle to be a type=\"button\" (no implicit form submit)")
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
		if c := strings.Count(body, `data-tag-filter-toggle`); c != 1 {
			t.Errorf("expected exactly 1 data-tag-filter-toggle marker, got %d", c)
		}
	})
}

// TestItemsTagSelectedState は Issue #117 の要件 1.4 / 4.3 を担保する。
//
//   - 現在の絞り込みに含まれているタグ（SelectedTags が true を返す NormalizedName）には
//     aria-pressed="true" と is-selected クラスが付与される
//   - 含まれていないタグには aria-pressed="false" が付与され is-selected は付かない
//   - これは JS (static/items_tags.js) が初期 SSR DOM 上で「選択中」の判定に依拠する
//     契約であり、JS 経由で再描画されるフラグメントでも同じ規約で出力される
func TestItemsTagSelectedState(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	render := func(t *testing.T, selected map[string]bool) string {
		t.Helper()
		r, err := New("../../templates")
		if err != nil {
			t.Fatalf("failed to create renderer: %v", err)
		}
		data := map[string]interface{}{
			"Title": "記事一覧",
			"Items": []testItemRow{{
				ID:          "item-sel",
				URL:         "https://example.com/sel",
				Title:       "選択テスト",
				Excerpt:     "e",
				FetchStatus: "completed",
				CreatedAt:   now,
				Tags: []testItemTag{
					{Name: "Go", NormalizedName: "go"},
					{Name: "Rust", NormalizedName: "rust"},
				},
			}},
			"Tags":           []testItemTag{},
			"SelectedTags":   selected,
			"Page":           1,
			"PerPage":        30,
			"Total":          1,
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

	t.Run("選択中のタグに aria-pressed=true と is-selected が付く (要件 1.4)", func(t *testing.T) {
		body := render(t, map[string]bool{"go": true})
		// "go" は選択中 → aria-pressed="true" + is-selected
		if !strings.Contains(body, `data-tag-normalized="go"`) {
			t.Fatalf("expected go button to be rendered")
		}
		// aria-pressed="true" がいずれかの button に現れる
		if !strings.Contains(body, `aria-pressed="true"`) {
			t.Errorf("expected at least one aria-pressed=\"true\" in body:\n%s", body)
		}
		if !strings.Contains(body, `is-selected`) {
			t.Errorf("expected is-selected class on selected tag button:\n%s", body)
		}
	})

	t.Run("非選択タグには aria-pressed=false が付き is-selected は付かない (要件 1.4 反対側)", func(t *testing.T) {
		body := render(t, map[string]bool{})
		// 非選択タグのみ → "is-selected" は登場しない
		if strings.Contains(body, `is-selected`) {
			t.Errorf("did not expect is-selected when no tags are selected:\n%s", body)
		}
		// 全ての button に aria-pressed="false" が付く（2 件 = 2 個）
		if c := strings.Count(body, `aria-pressed="false"`); c != 2 {
			t.Errorf("expected aria-pressed=\"false\" on both tags, got %d:\n%s", c, body)
		}
	})

	t.Run("複数選択中のタグそれぞれに aria-pressed=true が付く (要件 1.4 / 2.3 整合)", func(t *testing.T) {
		body := render(t, map[string]bool{"go": true, "rust": true})
		if c := strings.Count(body, `aria-pressed="true"`); c != 2 {
			t.Errorf("expected aria-pressed=\"true\" on both selected tags, got %d:\n%s", c, body)
		}
		if c := strings.Count(body, `is-selected`); c != 2 {
			t.Errorf("expected is-selected on both selected tags, got %d:\n%s", c, body)
		}
	})
}
