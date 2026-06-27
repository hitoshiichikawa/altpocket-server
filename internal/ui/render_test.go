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
				"Title":         "記事一覧",
				"Page":          1,
				"TotalPages":    1,
				"PerPage":       30,
				"Sort":          "newest",
				"StatusTab":     "unread",
				"StatusTabURLs": testStatusTabURLs(),
				"StatusQuery":   "",
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
	// Status mirrors store.Item.Status (the user-visible lifecycle state
	// introduced by Issue #119). items_list.html references `.Status` on every
	// item row (status badge / mark-read / archive toggle), so the test row
	// type must carry it or the template render fails with "can't evaluate
	// field Status".
	Status    string
	CreatedAt time.Time
	Tags      []testItemTag
}

// testStatusTabURLs returns the per-tab navigation URLs that items.html's
// status-tabs nav reads via `{{index .StatusTabURLs "unread"}}` (Issue #119).
// handleUIItems always sets this map (server.go: buildStatusTabURLs), so the
// older items-page render tests must supply it too; a missing key would leave
// the template indexing an untyped nil and fail the render.
func testStatusTabURLs() map[string]string {
	return map[string]string{
		"unread":   "/ui/items?status=unread",
		"all":      "/ui/items?status=all",
		"archived": "/ui/items?status=archived",
	}
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
		"StatusTab":      "unread",
		"StatusTabURLs":  testStatusTabURLs(),
		"StatusQuery":    "",
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
				Status:      "unread",
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
		Status:      "unread",
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
			Status:      "unread",
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
			Status:      "unread",
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
				Status:      "unread",
				CreatedAt:   now,
				Tags:        nil,
			},
			{
				ID:          "item-tagged",
				URL:         "https://example.com/b",
				Title:       "タグあり記事",
				Excerpt:     "e2",
				FetchStatus: "completed",
				Status:      "unread",
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
				Status:      "unread",
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
			"StatusTab":      "unread",
			"StatusTabURLs":  testStatusTabURLs(),
			"StatusQuery":    "",
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

// testActiveFilter is a minimal stand-in for the server.ActiveTagFilter type
// used to drive the SSR template tests for Issue #115 without depending on
// the server package.
type testActiveFilter struct {
	Name           string
	NormalizedName string
	RemoveURL      string
}

// renderItemsWithActiveFilters renders the full /ui/items page with the
// supplied ActiveTagFilters list so the template's chip row can be
// asserted. Items is intentionally non-empty so the chip row is rendered
// above a normal list (Req 1.1: above the result list).
func renderItemsWithActiveFilters(t *testing.T, active []testActiveFilter, clearAllURL string) string {
	t.Helper()
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	r, err := New("../../templates")
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}
	data := map[string]interface{}{
		"Title": "記事一覧",
		"Items": []testItemRow{{
			ID:          "item-x",
			URL:         "https://example.com/x",
			Title:       "x",
			Excerpt:     "e",
			FetchStatus: "completed",
			Status:      "unread",
			CreatedAt:   now,
		}},
		"Tags":             []testItemTag{},
		"SelectedTags":     map[string]bool{},
		"ActiveTagFilters": active,
		"ClearAllTagsURL":  clearAllURL,
		"Page":             1,
		"PerPage":          30,
		"Total":            1,
		"TotalPages":       1,
		"Query":            "",
		"Sort":             "newest",
		"PerPageOptions":   []int{10, 20, 30, 40, 50},
		"PrevURL":          "",
		"NextURL":          "",
		"StatusTab":        "unread",
		"StatusTabURLs":    testStatusTabURLs(),
		"StatusQuery":      "",
	}
	rr := httptest.NewRecorder()
	if err := r.Render(rr, "items", data); err != nil {
		t.Fatalf("render error: %v", err)
	}
	return rr.Body.String()
}

// TestActiveFiltersRendering covers Issue #115 SSR contract:
//
//   - Req 1.1: chip row is rendered above the items list when at least one
//     filter is active
//   - Req 1.2: chip row is NOT rendered when there are no active filters
//   - Req 1.3: each chip carries the display name (.Name)
//   - Req 1.4: each chip is itself the remove control (the `<a>` element)
//   - Req 3.1: a "Clear all" control is rendered next to the chip list
//   - Req 5.1: each chip's href is the canonical RemoveURL produced server-side
//   - Req 6.1 / 6.4 / 6.5: chips and the clear-all control are reachable by
//     keyboard (they are `<a href>`) and carry accessible names via
//     aria-label
//   - NFR 2.1: the SSR fallback works without JS (the chip and the clear-all
//     control are `<a href>` elements that perform full-page navigation on
//     plain click when JS is disabled)
func TestActiveFiltersRendering(t *testing.T) {
	t.Run("Req 1.2: zero active filters does not render chip row", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, nil, "/ui/items")
		if strings.Contains(body, `data-active-filters`) {
			t.Errorf("expected no data-active-filters marker when no filters are active, got body containing it")
		}
		if strings.Contains(body, `data-active-filter-chip`) {
			t.Errorf("expected no chips when no filters are active")
		}
		if strings.Contains(body, `active-filter-clear-all`) {
			t.Errorf("expected no clear-all control when no filters are active")
		}
	})

	t.Run("Req 1.1 / 1.3 / 1.4: one active filter renders one chip with display name and remove control", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items"},
		}, "/ui/items")
		if !strings.Contains(body, `data-active-filters`) {
			t.Errorf("expected data-active-filters marker, got:\n%s", body)
		}
		if c := strings.Count(body, `data-active-filter-chip`); c != 1 {
			t.Errorf("expected 1 chip, got %d:\n%s", c, body)
		}
		// Display name is rendered.
		if !strings.Contains(body, `>Go<`) {
			t.Errorf("expected display name 'Go' to appear in chip, got:\n%s", body)
		}
		// Normalized name is exposed for JS sync (data-tag-normalized).
		if !strings.Contains(body, `data-tag-normalized="go"`) {
			t.Errorf("expected data-tag-normalized=\"go\", got:\n%s", body)
		}
	})

	t.Run("Req 5.1 / NFR 2.1: chip is an <a href> pointing at the RemoveURL", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items?tag=rust"},
		}, "/ui/items")
		// The chip's href should be the RemoveURL exactly.
		if !strings.Contains(body, `href="/ui/items?tag=rust"`) {
			t.Errorf("expected chip href to be the RemoveURL, got:\n%s", body)
		}
	})

	t.Run("Req 6.4: chip carries aria-label with both the tag display name and the 'unset' intent", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items"},
		}, "/ui/items")
		// The aria-label must include the display name AND a phrase
		// conveying the intent that activating the control will unset
		// the filter. The exact Japanese wording is checked because we
		// rely on it for screen-reader UX.
		if !strings.Contains(body, `aria-label="フィルタ解除: Go"`) {
			t.Errorf("expected accessible name with tag display name and unset intent, got:\n%s", body)
		}
	})

	t.Run("Req 3.1: clear-all control is rendered when there is at least one chip", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items"},
		}, "/ui/items?q=keep")
		if !strings.Contains(body, `data-active-filter-clear-all`) {
			t.Errorf("expected data-active-filter-clear-all marker, got:\n%s", body)
		}
		if !strings.Contains(body, `href="/ui/items?q=keep"`) {
			t.Errorf("expected clear-all href to be ClearAllTagsURL, got:\n%s", body)
		}
	})

	t.Run("Req 6.5: clear-all control carries an accessible name describing 'unset all'", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items"},
		}, "/ui/items")
		if !strings.Contains(body, `aria-label="すべてのフィルタを解除"`) {
			t.Errorf("expected clear-all aria-label, got:\n%s", body)
		}
	})

	t.Run("Req 1.1 / 1.5: multiple active filters render multiple chips in order", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items?tag=rust"},
			{Name: "Rust", NormalizedName: "rust", RemoveURL: "/ui/items?tag=go"},
		}, "/ui/items")
		if c := strings.Count(body, `data-active-filter-chip`); c != 2 {
			t.Errorf("expected 2 chips, got %d:\n%s", c, body)
		}
		// Order is preserved (Go appears before Rust).
		goIdx := strings.Index(body, `data-tag-normalized="go"`)
		rustIdx := strings.Index(body, `data-tag-normalized="rust"`)
		if goIdx < 0 || rustIdx < 0 {
			t.Fatalf("expected both chips to be rendered, got go=%d rust=%d", goIdx, rustIdx)
		}
		if goIdx > rustIdx {
			t.Errorf("expected go to appear before rust in DOM order, got go=%d rust=%d", goIdx, rustIdx)
		}
	})

	t.Run("Req 1.1 (placement): chip row appears above the items list", func(t *testing.T) {
		body := renderItemsWithActiveFilters(t, []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items"},
		}, "/ui/items")
		filterIdx := strings.Index(body, `data-active-filters`)
		itemIdx := strings.Index(body, `id="item-title-item-x"`)
		if filterIdx < 0 || itemIdx < 0 {
			t.Fatalf("expected both chip row and item card to be rendered, got filter=%d item=%d", filterIdx, itemIdx)
		}
		if filterIdx > itemIdx {
			t.Errorf("expected chip row to appear before item card in DOM order, got filter=%d item=%d", filterIdx, itemIdx)
		}
	})
}

// TestActiveFiltersFragmentRendering covers Req 4.3 / 4.5: the fragment
// returned by the fragment endpoint must also contain the chip row so the
// client side `region.innerHTML = html` swap keeps the chip row in sync
// with the URL.
func TestActiveFiltersFragmentRendering(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
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
			Status:      "unread",
			CreatedAt:   now,
		}},
		"ActiveTagFilters": []testActiveFilter{
			{Name: "Go", NormalizedName: "go", RemoveURL: "/ui/items"},
		},
		"ClearAllTagsURL": "/ui/items",
		"Page":            1,
		"TotalPages":      1,
		"PrevURL":         "",
		"NextURL":         "",
	}
	rr := httptest.NewRecorder()
	if err := r.RenderFragment(rr, "items_list", data); err != nil {
		t.Fatalf("RenderFragment error: %v", err)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-active-filters`) {
		t.Errorf("expected fragment to include chip row marker, got:\n%s", body)
	}
	if !strings.Contains(body, `data-tag-normalized="go"`) {
		t.Errorf("expected fragment to include chip for go, got:\n%s", body)
	}
}

// testSidebarTag mirrors store.Tag's template-visible fields for the sidebar
// tag-list (`.Tags` range). The sidebar option template references .Name,
// .NormalizedName and .Count, so the test row type must carry all three.
type testSidebarTag struct {
	Name           string
	NormalizedName string
	Count          int
}

// renderItemsWithSidebarTags renders the full `items` page with both a set of
// item cards and a sidebar tag list, returning the HTML body. Used by the
// Issue #120 drag-and-drop tagging tests which assert on the SSR contract that
// the client JS (static/items_drag_tag.js) relies on.
func renderItemsWithSidebarTags(t *testing.T, items []testItemRow, sidebarTags []testSidebarTag) string {
	t.Helper()
	r, err := New("../../templates")
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}
	data := map[string]interface{}{
		"Title":           "記事一覧",
		"Items":           items,
		"Tags":            sidebarTags,
		"SelectedTags":    map[string]bool{},
		"Page":            1,
		"PerPage":         30,
		"Total":           len(items),
		"TotalPages":      1,
		"Query":           "",
		"Sort":            "newest",
		"PerPageOptions":  []int{10, 20, 30, 40, 50},
		"PrevURL":         "",
		"NextURL":         "",
		"StatusTab":       "unread",
		"StatusTabURLs":   testStatusTabURLs(),
		"StatusQuery":     "",
		"ClearFiltersURL": "/ui/items",
	}
	rr := httptest.NewRecorder()
	if err := r.Render(rr, "items", data); err != nil {
		t.Fatalf("render error: %v", err)
	}
	return rr.Body.String()
}

// TestDragTagSSRContract は Issue #120 のドラッグ&ドロップ・タグ付与が依拠する
// SSR 契約を担保する。client JS (static/items_drag_tag.js) は以下の DOM 契約に
// 依拠してドラッグ元 / ドロップ先 / タッチ代替手段を解決する:
//
//   - 各カードが draggable="true" でドラッグ可能 (Req 1.1)
//   - 各カードがアイテム ID を JS から読めるよう data-item-id を持つ (Req 1.3 / 2.1)
//   - 各カードにタッチ代替手段のトリガ button[data-card-tag-add] がある (Req 4.1)
//   - サイドバー / ボトムシートのタグ要素がドロップ先として
//     data-tag-drop-target + data-tag-name (display) + data-tag-normalized を持つ
//     (Req 1.2 / 3.4 / 3.5)
//   - 専用スクリプト items_drag_tag.js が読み込まれる
func TestDragTagSSRContract(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	body := renderItemsWithSidebarTags(t,
		[]testItemRow{{
			ID:          "item-drag",
			URL:         "https://example.com/a",
			Title:       "ドラッグ記事",
			Excerpt:     "本文抜粋",
			FetchStatus: "completed",
			Status:      "unread",
			CreatedAt:   now,
			Tags:        []testItemTag{{Name: "Go", NormalizedName: "go"}},
		}},
		[]testSidebarTag{
			{Name: "Go", NormalizedName: "go", Count: 3},
			{Name: "Rust 言語", NormalizedName: "rust 言語", Count: 1},
		},
	)

	t.Run("Req 1.1: カードが draggable=true でドラッグ可能", func(t *testing.T) {
		if !strings.Contains(body, `draggable="true"`) {
			t.Errorf("expected item card to be draggable, got:\n%s", body)
		}
	})

	t.Run("Req 1.3 / 2.1: カードがアイテム ID を data-item-id で公開", func(t *testing.T) {
		if !strings.Contains(body, `data-item-id="item-drag"`) {
			t.Errorf("expected card to expose data-item-id, got:\n%s", body)
		}
	})

	t.Run("Req 4.1: カードにタッチ代替手段のトリガ button[data-card-tag-add] がある", func(t *testing.T) {
		if !strings.Contains(body, `data-card-tag-add`) {
			t.Errorf("expected card to carry a touch tag-add trigger, got:\n%s", body)
		}
		// 代替手段はカードのアイテム ID を JS に渡せる必要がある。
		if !strings.Contains(body, `data-card-tag-add data-item-id="item-drag"`) &&
			!strings.Contains(body, `data-item-id="item-drag" data-card-tag-add`) {
			t.Errorf("expected touch trigger to carry data-item-id, got:\n%s", body)
		}
	})

	t.Run("Req 1.2 / 3.4: サイドバーのタグ要素がドロップ先 + data-tag-name / data-tag-normalized を持つ", func(t *testing.T) {
		// ドロップ先マーカー。サイドバー + ボトムシートで重複描画されるため 2 件以上。
		if c := strings.Count(body, `data-tag-drop-target`); c < 2 {
			t.Errorf("expected at least 2 drop-target markers (sidebar + sheet), got %d:\n%s", c, body)
		}
		// display name (Req 4.2: テキストラベルで識別可能 / 色のみに依存しない)
		if !strings.Contains(body, `data-tag-name="Go"`) {
			t.Errorf("expected drop target to carry display name, got:\n%s", body)
		}
		// normalized name は絞り込み checkbox の value / chip 選択状態の判定に使う
		// （bulk-tag へ送るのは data-tag-name の display 名）。
		if !strings.Contains(body, `data-tag-normalized="go"`) {
			t.Errorf("expected drop target to carry normalized name, got:\n%s", body)
		}
	})

	t.Run("Req 3.5: 表示名にスペースを含むタグでも display / normalized 双方が描画される", func(t *testing.T) {
		if !strings.Contains(body, `data-tag-name="Rust 言語"`) {
			t.Errorf("expected drop target display name with space, got:\n%s", body)
		}
		if !strings.Contains(body, `data-tag-normalized="rust 言語"`) {
			t.Errorf("expected drop target normalized name with space, got:\n%s", body)
		}
	})

	t.Run("専用スクリプト items_drag_tag.js が読み込まれる", func(t *testing.T) {
		if !strings.Contains(body, `/static/items_drag_tag.js`) {
			t.Errorf("expected items_drag_tag.js to be included, got:\n%s", body)
		}
	})
}
