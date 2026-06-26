package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"altpocket/internal/store"
)

func TestPerPageValue(t *testing.T) {
	if perPageValue("10") != 10 {
		t.Fatalf("expected 10")
	}
	if perPageValue("35") != 30 {
		t.Fatalf("invalid should default to 30")
	}
	if perPageValue("") != 30 {
		t.Fatalf("empty should default to 30")
	}
}

func TestDefaultSort(t *testing.T) {
	if defaultSort("relevance") != "relevance" {
		t.Fatalf("expected relevance")
	}
	if defaultSort("other") != "newest" {
		t.Fatalf("expected newest")
	}
}

func TestPageURL(t *testing.T) {
	u, _ := url.Parse("http://example.com/ui/items?q=go")
	got := pageURL(u, 2)
	if got != "http://example.com/ui/items?q=go&page=2" && got != "http://example.com/ui/items?page=2&q=go" {
		t.Fatalf("unexpected url: %s", got)
	}
}

// TestWantsItemsFragment guards the dispatch contract that lets
// handleUIItems return an HTML fragment for the search-debounce / URL sync
// flow (Issue #114).
//
// The handler must:
//   - render the full /ui/items page when no opt-in header is present
//     (preserves the current SSR contract, NFR 2)
//   - render the items_list fragment only when X-Requested-With: ItemsFragment
//     is supplied by the client-side JS
func TestWantsItemsFragment(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{name: "no header", header: "", want: false},
		{name: "exact match", header: "ItemsFragment", want: true},
		{name: "case-insensitive match", header: "itemsfragment", want: true},
		{name: "alternate casing", header: "ITEMSFRAGMENT", want: true},
		{name: "unrelated XHR value (legacy XMLHttpRequest)", header: "XMLHttpRequest", want: false},
		{name: "different value", header: "fragment", want: false},
		{name: "value with spaces is rejected", header: " ItemsFragment ", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ui/items", nil)
			if tc.header != "" {
				req.Header.Set("X-Requested-With", tc.header)
			}
			got := wantsItemsFragment(req)
			if got != tc.want {
				t.Fatalf("wantsItemsFragment(header=%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}

	t.Run("nil request returns false", func(t *testing.T) {
		if wantsItemsFragment(nil) {
			t.Fatalf("expected nil request to be treated as full-page render")
		}
	})
}

func TestParseTagInput(t *testing.T) {
	// parseTagInput now only splits — normalization and dedup happen downstream
	// in normalizeTagInputs so the original casing reaches tags.name
	// (Issue #115 / AC 1.3).
	got := parseTagInput(" Go,news;go\nweb ")
	if len(got) != 4 {
		t.Fatalf("expected 4 raw tokens (no dedup), got %d: %#v", len(got), got)
	}
	if got[0] != " Go" || got[1] != "news" || got[2] != "go" || got[3] != "web " {
		t.Fatalf("unexpected raw tokens: %#v", got)
	}
}

func TestNormalizeTagInputs(t *testing.T) {
	// normalizeTagInputs subsumes the previous combined behaviour of
	// parseTagInput: trim+NFKC for the display Name, lowercase NormalizedName
	// for the key, dedup by normalized key with first-display-wins (Issue #115
	// / AC 1.3).
	t.Run("preserves user casing while normalizing the key", func(t *testing.T) {
		got := normalizeTagInputs([]string{"Go Lang", "Rust-Lang"})
		if len(got) != 2 {
			t.Fatalf("expected 2 inputs, got %d: %#v", len(got), got)
		}
		if got[0].Name != "Go Lang" || got[0].NormalizedName != "go lang" {
			t.Errorf("got[0] = (%q, %q), want (Go Lang, go lang)", got[0].Name, got[0].NormalizedName)
		}
		if got[1].Name != "Rust-Lang" || got[1].NormalizedName != "rust-lang" {
			t.Errorf("got[1] = (%q, %q), want (Rust-Lang, rust-lang)", got[1].Name, got[1].NormalizedName)
		}
	})

	t.Run("dedups by normalized key and keeps the first display", func(t *testing.T) {
		got := normalizeTagInputs([]string{"Go Lang", "go lang", "GO LANG"})
		if len(got) != 1 {
			t.Fatalf("expected 1 input after dedup, got %d: %#v", len(got), got)
		}
		if got[0].Name != "Go Lang" || got[0].NormalizedName != "go lang" {
			t.Errorf("got[0] = (%q, %q), want first-display (Go Lang, go lang)", got[0].Name, got[0].NormalizedName)
		}
	})

	t.Run("drops empty / whitespace-only entries", func(t *testing.T) {
		got := normalizeTagInputs([]string{"", "   ", " Go ", "\t"})
		if len(got) != 1 {
			t.Fatalf("expected 1 input, got %d: %#v", len(got), got)
		}
		if got[0].Name != "Go" || got[0].NormalizedName != "go" {
			t.Errorf("got[0] = (%q, %q), want (Go, go)", got[0].Name, got[0].NormalizedName)
		}
	})

	t.Run("NFKC folds fullwidth chars while preserving case", func(t *testing.T) {
		got := normalizeTagInputs([]string{"ＧｏＬａｎｇ"})
		if len(got) != 1 {
			t.Fatalf("expected 1 input, got %d: %#v", len(got), got)
		}
		if got[0].Name != "GoLang" || got[0].NormalizedName != "golang" {
			t.Errorf("got[0] = (%q, %q), want (GoLang, golang)", got[0].Name, got[0].NormalizedName)
		}
	})
}

func TestParseTagFilters(t *testing.T) {
	values := url.Values{}
	values.Add("tag", "Go")
	values.Add("tag", "news")
	values.Set("tags", "web,go")
	got := parseTagFilters(values)
	if len(got) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(got))
	}
	if got[0] != "go" || got[1] != "news" || got[2] != "web" {
		t.Fatalf("unexpected tags: %#v", got)
	}
}

func TestSelectedTagSet(t *testing.T) {
	set := selectedTagSet([]string{"go", "news"})
	if !set["go"] || !set["news"] {
		t.Fatalf("expected selected tags to be true: %#v", set)
	}
	if set["web"] {
		t.Fatalf("unexpected selected tag: %#v", set)
	}
}

// TestBuildClearAllTagsURL guards Req 3.6 / 5.2 / 5.3: clearing all tag
// filters removes every `tag` / `tags` query parameter while preserving the
// remaining query parameters (q / sort / per_page / page).
func TestBuildClearAllTagsURL(t *testing.T) {
	t.Run("removes tag and tags parameters", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?tag=go&tag=rust&tags=news,web&q=hello&sort=relevance&per_page=20&page=3")
		got, _ := url.Parse(buildClearAllTagsURL(u))
		if v := got.Query()["tag"]; len(v) != 0 {
			t.Errorf("expected no tag params, got %#v", v)
		}
		if v := got.Query()["tags"]; len(v) != 0 {
			t.Errorf("expected no tags params, got %#v", v)
		}
	})

	t.Run("preserves other query parameters including page (Req 5.2)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?tag=go&q=hello&sort=relevance&per_page=20&page=3")
		got, _ := url.Parse(buildClearAllTagsURL(u))
		if got.Query().Get("q") != "hello" {
			t.Errorf("expected q=hello preserved, got %q", got.Query().Get("q"))
		}
		if got.Query().Get("sort") != "relevance" {
			t.Errorf("expected sort=relevance preserved, got %q", got.Query().Get("sort"))
		}
		if got.Query().Get("per_page") != "20" {
			t.Errorf("expected per_page=20 preserved, got %q", got.Query().Get("per_page"))
		}
		if got.Query().Get("page") != "3" {
			t.Errorf("expected page=3 preserved, got %q", got.Query().Get("page"))
		}
	})

	t.Run("no tag filter present is no-op for tag/tags", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?q=hello")
		got, _ := url.Parse(buildClearAllTagsURL(u))
		if got.Query().Get("q") != "hello" {
			t.Errorf("expected q=hello preserved, got %q", got.Query().Get("q"))
		}
	})
}

// TestBuildTagRemovedURL guards Req 2.1 / 2.5 / 2.6 / 5.1 / 5.2 / 5.3:
// removing a single tag preserves the remaining tags in the canonical
// `?tag=` repetition form, drops the legacy `?tags=` plural form, and
// removes the `tag` parameter entirely when the resulting set is empty.
func TestBuildTagRemovedURL(t *testing.T) {
	t.Run("removes one tag and keeps the others in canonical form", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?tag=go&tag=rust&tag=news")
		got, _ := url.Parse(buildTagRemovedURL(u, "rust", []string{"go", "rust", "news"}))
		tags := got.Query()["tag"]
		if len(tags) != 2 {
			t.Errorf("expected 2 remaining tags, got %#v", tags)
		}
		if tags[0] != "go" || tags[1] != "news" {
			t.Errorf("expected [go, news], got %#v", tags)
		}
		if v := got.Query()["tags"]; len(v) != 0 {
			t.Errorf("expected no legacy tags param, got %#v", v)
		}
	})

	t.Run("last tag removed strips tag parameter entirely (Req 2.5 / 5.3)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?tag=go&q=hello")
		got, _ := url.Parse(buildTagRemovedURL(u, "go", []string{"go"}))
		if v := got.Query()["tag"]; len(v) != 0 {
			t.Errorf("expected tag to be removed, got %#v", v)
		}
		if got.Query().Get("q") != "hello" {
			t.Errorf("expected q=hello preserved, got %q", got.Query().Get("q"))
		}
		if strings.Contains(got.RawQuery, "tag=") {
			t.Errorf("expected raw query to not contain tag= but got %q", got.RawQuery)
		}
	})

	t.Run("legacy ?tags=csv is migrated to canonical ?tag= repetition (Req 5.1)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?tags=go,rust,news")
		got, _ := url.Parse(buildTagRemovedURL(u, "rust", []string{"go", "rust", "news"}))
		tags := got.Query()["tag"]
		if len(tags) != 2 {
			t.Errorf("expected 2 remaining tags in canonical form, got %#v", tags)
		}
		if got.Query().Get("tags") != "" {
			t.Errorf("expected legacy ?tags= to be dropped, got %q", got.Query().Get("tags"))
		}
	})

	t.Run("preserves q / sort / per_page / page (Req 5.2)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?tag=go&tag=rust&q=hello&sort=relevance&per_page=20&page=3")
		got, _ := url.Parse(buildTagRemovedURL(u, "go", []string{"go", "rust"}))
		if got.Query().Get("q") != "hello" {
			t.Errorf("expected q=hello preserved, got %q", got.Query().Get("q"))
		}
		if got.Query().Get("sort") != "relevance" {
			t.Errorf("expected sort=relevance preserved, got %q", got.Query().Get("sort"))
		}
		if got.Query().Get("per_page") != "20" {
			t.Errorf("expected per_page=20 preserved, got %q", got.Query().Get("per_page"))
		}
		if got.Query().Get("page") != "3" {
			t.Errorf("expected page=3 preserved, got %q", got.Query().Get("page"))
		}
	})

	t.Run("nil current URL is treated as empty path", func(t *testing.T) {
		// Defensive: the helper accepts nil to avoid panics in tests using
		// httptest.NewRequest where the URL field is always populated, but
		// we still want a deterministic result.
		got := buildTagRemovedURL(nil, "go", []string{"go"})
		if got != "" {
			t.Errorf("expected empty URL string for nil input, got %q", got)
		}
	})
}

// TestBuildActiveTagFilters guards Req 1.1 / 1.3 / 1.5 / 5.4: chips are
// produced one-to-one with the active tag filter set, the display name is
// resolved from the Tags facet first and then the items' Tags as fallback,
// and an unresolved name falls back to the normalized form.
func TestBuildActiveTagFilters(t *testing.T) {
	currentURL, _ := url.Parse("/ui/items?tag=go&tag=rust")

	t.Run("Req 1.2: zero filters returns nil", func(t *testing.T) {
		got := buildActiveTagFilters(nil, nil, nil, currentURL)
		if len(got) != 0 {
			t.Errorf("expected no chips, got %#v", got)
		}
		got = buildActiveTagFilters([]string{}, nil, nil, currentURL)
		if len(got) != 0 {
			t.Errorf("expected no chips for empty slice, got %#v", got)
		}
	})

	t.Run("display name resolved from Tags facet (full-page)", func(t *testing.T) {
		facet := []store.Tag{
			{NormalizedName: "go", Name: "Go"},
			{NormalizedName: "rust", Name: "Rust"},
		}
		got := buildActiveTagFilters([]string{"go", "rust"}, facet, nil, currentURL)
		if len(got) != 2 {
			t.Fatalf("expected 2 chips, got %d", len(got))
		}
		if got[0].Name != "Go" || got[0].NormalizedName != "go" {
			t.Errorf("chip[0] = %#v, want Name=Go NormalizedName=go", got[0])
		}
		if got[1].Name != "Rust" || got[1].NormalizedName != "rust" {
			t.Errorf("chip[1] = %#v, want Name=Rust NormalizedName=rust", got[1])
		}
	})

	t.Run("display name falls back to items' Tags when facet is empty (fragment path)", func(t *testing.T) {
		items := []store.ItemListRow{{
			Tags: []store.Tag{
				{NormalizedName: "go", Name: "Go"},
				{NormalizedName: "rust", Name: "Rust"},
			},
		}}
		got := buildActiveTagFilters([]string{"go", "rust"}, nil, items, currentURL)
		if len(got) != 2 {
			t.Fatalf("expected 2 chips, got %d", len(got))
		}
		if got[0].Name != "Go" {
			t.Errorf("expected fallback to per-item Tag name, got %q", got[0].Name)
		}
		if got[1].Name != "Rust" {
			t.Errorf("expected fallback to per-item Tag name, got %q", got[1].Name)
		}
	})

	t.Run("unresolved tag falls back to normalized name", func(t *testing.T) {
		got := buildActiveTagFilters([]string{"unresolved"}, nil, nil, currentURL)
		if len(got) != 1 {
			t.Fatalf("expected 1 chip, got %d", len(got))
		}
		if got[0].Name != "unresolved" {
			t.Errorf("expected fallback to normalized name, got %q", got[0].Name)
		}
		if got[0].NormalizedName != "unresolved" {
			t.Errorf("expected NormalizedName=unresolved, got %q", got[0].NormalizedName)
		}
	})

	t.Run("Req 1.5: chip order matches the tagFilters input order", func(t *testing.T) {
		facet := []store.Tag{
			{NormalizedName: "rust", Name: "Rust"},
			{NormalizedName: "go", Name: "Go"},
		}
		got := buildActiveTagFilters([]string{"go", "rust"}, facet, nil, currentURL)
		if got[0].NormalizedName != "go" || got[1].NormalizedName != "rust" {
			t.Errorf("expected order to follow tagFilters [go, rust], got [%s, %s]",
				got[0].NormalizedName, got[1].NormalizedName)
		}
	})

	t.Run("each chip has RemoveURL with itself removed and others preserved (Req 5.1)", func(t *testing.T) {
		u, _ := url.Parse("/ui/items?tag=go&tag=rust&q=hello")
		got := buildActiveTagFilters([]string{"go", "rust"}, nil, nil, u)
		// Chip "go" should produce a URL with only rust remaining.
		goRemove, _ := url.Parse(got[0].RemoveURL)
		if tags := goRemove.Query()["tag"]; len(tags) != 1 || tags[0] != "rust" {
			t.Errorf("expected go's RemoveURL to have only [rust], got %#v", tags)
		}
		if goRemove.Query().Get("q") != "hello" {
			t.Errorf("expected q=hello preserved in RemoveURL, got %q", goRemove.Query().Get("q"))
		}
		// Chip "rust" should produce a URL with only go remaining.
		rustRemove, _ := url.Parse(got[1].RemoveURL)
		if tags := rustRemove.Query()["tag"]; len(tags) != 1 || tags[0] != "go" {
			t.Errorf("expected rust's RemoveURL to have only [go], got %#v", tags)
		}
	})

	t.Run("facet takes priority over per-item display name", func(t *testing.T) {
		// Simulate a case where the facet has the canonical user-entered
		// casing ("Go-Lang") and the per-item Tags have a different value.
		facet := []store.Tag{{NormalizedName: "go-lang", Name: "Go-Lang"}}
		items := []store.ItemListRow{{
			Tags: []store.Tag{{NormalizedName: "go-lang", Name: "golang"}},
		}}
		got := buildActiveTagFilters([]string{"go-lang"}, facet, items, currentURL)
		if got[0].Name != "Go-Lang" {
			t.Errorf("expected facet display name to win, got %q", got[0].Name)
		}
	})

	t.Run("Req 1.3 (regression): fragment-mode zero-result lookup still resolves display name", func(t *testing.T) {
		// Round-2 review case: the fragment path skips ListTagsWithCountFiltered
		// for performance, but the active filter chip must still show the
		// user-entered name (e.g. "Go Lang"). The handler now calls
		// store.TagsByNormalizedNames in fragment mode and feeds the result
		// into tagsForLookup, so even when `items` is empty (zero-result
		// filter) buildActiveTagFilters can resolve the display name from
		// the direct tag lookup rather than degrading to the normalized form.
		tagsLookup := []store.Tag{{NormalizedName: "go-lang", Name: "Go Lang"}}
		got := buildActiveTagFilters([]string{"go-lang"}, tagsLookup, nil, currentURL)
		if len(got) != 1 {
			t.Fatalf("expected 1 chip, got %d", len(got))
		}
		if got[0].Name != "Go Lang" {
			t.Errorf("expected display name from direct tag lookup, got %q (regression to normalized form)", got[0].Name)
		}
		if got[0].NormalizedName != "go-lang" {
			t.Errorf("expected NormalizedName=go-lang, got %q", got[0].NormalizedName)
		}
	})
}

func TestMergeTagDisplaySources(t *testing.T) {
	facet := []store.Tag{{NormalizedName: "go", Name: "Go"}}
	named := []store.Tag{{NormalizedName: "rust", Name: "Rust"}}

	t.Run("nil secondary returns primary unchanged", func(t *testing.T) {
		got := mergeTagDisplaySources(facet, nil)
		if len(got) != 1 || got[0].Name != "Go" {
			t.Errorf("expected primary unchanged, got %#v", got)
		}
	})

	t.Run("nil primary returns secondary unchanged", func(t *testing.T) {
		got := mergeTagDisplaySources(nil, named)
		if len(got) != 1 || got[0].Name != "Rust" {
			t.Errorf("expected secondary unchanged, got %#v", got)
		}
	})

	t.Run("primary precedes secondary so earlier-source-wins keeps facet casing", func(t *testing.T) {
		// Same normalized name in both: facet (primary) carries canonical
		// casing and must win in buildActiveTagFilters' dedup.
		primary := []store.Tag{{NormalizedName: "go-lang", Name: "Go-Lang"}}
		secondary := []store.Tag{{NormalizedName: "go-lang", Name: "go lang"}}
		got := mergeTagDisplaySources(primary, secondary)
		if len(got) != 2 {
			t.Fatalf("expected both entries retained, got %d", len(got))
		}
		if got[0].Name != "Go-Lang" {
			t.Errorf("expected primary first, got %q", got[0].Name)
		}
		chips := buildActiveTagFilters([]string{"go-lang"}, got, nil, mustURL("/ui/items?tag=go-lang"))
		if chips[0].Name != "Go-Lang" {
			t.Errorf("expected facet casing to win, got %q", chips[0].Name)
		}
	})
}

// TestFullPageZeroResultResolvesDisplayName reproduces the round-3 review
// regression (Issue #115) at the pure-function level so it is guarded even when
// TEST_DATABASE_URL is unset. On the full-page path a zero-result tag AND
// filter leaves the facet (ListTagsWithCountFiltered) empty; without merging in
// the direct TagsByNormalizedNames lookup the chip degrades to the normalized
// lowercase form, violating AC 1.3 (chips show the original display name) and
// AC 4.5 (direct URL open matches the query).
func TestFullPageZeroResultResolvesDisplayName(t *testing.T) {
	// facet is empty because the go+rust AND-condition matches zero items.
	var facet []store.Tag
	// Direct lookup still resolves the user-entered display names.
	named := []store.Tag{
		{NormalizedName: "go lang", Name: "Go Lang"},
		{NormalizedName: "rust lang", Name: "Rust Lang"},
	}
	tagsForLookup := mergeTagDisplaySources(facet, named)

	tagFilters := []string{"go lang", "rust lang"}
	chips := buildActiveTagFilters(tagFilters, tagsForLookup, nil, mustURL("/ui/items?tag=go+lang&tag=rust+lang"))

	if len(chips) != 2 {
		t.Fatalf("expected 2 chips, got %d: %#v", len(chips), chips)
	}
	byNorm := map[string]string{}
	for _, c := range chips {
		byNorm[c.NormalizedName] = c.Name
	}
	if got := byNorm["go lang"]; got != "Go Lang" {
		t.Errorf("chip for 'go lang' = %q, want %q (regression to normalized form)", got, "Go Lang")
	}
	if got := byNorm["rust lang"]; got != "Rust Lang" {
		t.Errorf("chip for 'rust lang' = %q, want %q (regression to normalized form)", got, "Rust Lang")
	}
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func TestNormalizeWhitespace(t *testing.T) {
	got := normalizeWhitespace("  hello \n  world\tgo  ")
	if got != "hello world go" {
		t.Fatalf("unexpected normalized text: %q", got)
	}
}

func TestTruncateUTF8(t *testing.T) {
	got := truncateUTF8("あいうえお", 7)
	if got != "あい" {
		t.Fatalf("expected UTF-8 safe truncation, got %q", got)
	}
}

func TestCreateItemPrefillTitleTruncation(t *testing.T) {
	input := strings.Repeat("a", 600)
	got := truncateUTF8(strings.TrimSpace(input), 500)
	if len(got) != 500 {
		t.Fatalf("expected title truncated to 500 bytes, got %d", len(got))
	}
}

func TestCreateItemPrefillExcerptTruncation(t *testing.T) {
	input := strings.Repeat("b", 300)
	got := truncateUTF8(strings.TrimSpace(input), 200)
	if len(got) != 200 {
		t.Fatalf("expected excerpt truncated to 200 bytes, got %d", len(got))
	}
}

func TestCreateItemPrefillTrimSpace(t *testing.T) {
	got := truncateUTF8(strings.TrimSpace("  hello world  "), 500)
	if got != "hello world" {
		t.Fatalf("expected trimmed title, got %q", got)
	}
}

func TestCreateItemPrefillUTF8TitleTruncation(t *testing.T) {
	// 500 bytes should safely truncate multi-byte characters
	input := strings.Repeat("日", 200) // 200 chars × 3 bytes = 600 bytes
	got := truncateUTF8(strings.TrimSpace(input), 500)
	if len(got) > 500 {
		t.Fatalf("expected title ≤ 500 bytes, got %d", len(got))
	}
	// Should be valid UTF-8 (no broken runes)
	runes := []rune(got)
	if len(runes) != 166 { // 498 bytes / 3 bytes per rune = 166 full runes
		t.Fatalf("expected 166 runes (valid UTF-8), got %d", len(runes))
	}
}

func TestCreateItemPrefillEmptyValuesArePassthrough(t *testing.T) {
	title := truncateUTF8(strings.TrimSpace(""), 500)
	excerpt := truncateUTF8(strings.TrimSpace(""), 200)
	if title != "" {
		t.Fatalf("expected empty title, got %q", title)
	}
	if excerpt != "" {
		t.Fatalf("expected empty excerpt, got %q", excerpt)
	}
}

func TestQuickAddNotice(t *testing.T) {
	if quickAddNotice("created") == "" {
		t.Fatalf("created state should return notice")
	}
	if quickAddNotice("exists") == "" {
		t.Fatalf("exists state should return notice")
	}
	if quickAddNotice("other") != "" {
		t.Fatalf("unexpected notice for unknown state")
	}
}

func TestSettingsNotice(t *testing.T) {
	msg, cls := settingsNotice("export_success")
	if msg == "" || cls != "notice" {
		t.Fatalf("expected export_success notice, got msg=%q class=%q", msg, cls)
	}
	msg, cls = settingsNotice("export_failed")
	if msg == "" || cls != "error" {
		t.Fatalf("expected export_failed error, got msg=%q class=%q", msg, cls)
	}
	msg, cls = settingsNotice("unknown")
	if msg != "" || cls != "" {
		t.Fatalf("expected empty notice for unknown status")
	}
}

func TestGoogleSheetURL(t *testing.T) {
	if got := googleSheetURL(""); got != "" {
		t.Fatalf("expected empty url for blank id")
	}
	if got := googleSheetURL("sheet-id-123"); got != "https://docs.google.com/spreadsheets/d/sheet-id-123/edit" {
		t.Fatalf("unexpected sheet url: %q", got)
	}
}

func TestExtractHTTPURLFromText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain text", in: "hello world", want: ""},
		{name: "http url", in: "check http://example.com/page", want: "http://example.com/page"},
		{name: "https with punctuation", in: "see (https://example.com/path?q=1).", want: "https://example.com/path?q=1"},
		{name: "non http scheme ignored", in: "ftp://example.com", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractHTTPURLFromText(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestQuickAddContentPreview(t *testing.T) {
	got := quickAddContentPreview("https://example.com/abc", "Read https://example.com/abc later")
	if got != "Read later" {
		t.Fatalf("unexpected content preview: %q", got)
	}
}

func TestQuickAddContentPreviewTruncatesTo200Runes(t *testing.T) {
	input := strings.Repeat("あ", 220)
	got := quickAddContentPreview("", input)
	if len([]rune(got)) != 200 {
		t.Fatalf("expected 200 runes, got %d", len([]rune(got)))
	}
}

func TestSanitizeQuickAddContentNormalizesWhitespace(t *testing.T) {
	got := sanitizeQuickAddContent("  one \n two\tthree  ")
	if got != "one two three" {
		t.Fatalf("unexpected sanitized content: %q", got)
	}
}

func TestSanitizeQuickAddContentTruncatesTo200Runes(t *testing.T) {
	input := strings.Repeat("a", 220)
	got := sanitizeQuickAddContent(input)
	if len([]rune(got)) != 200 {
		t.Fatalf("expected 200 runes, got %d", len([]rune(got)))
	}
}

func TestSanitizeQuickAddContentInputUsesByteLimit(t *testing.T) {
	got := sanitizeQuickAddContentInput("  abc\n def  ", 7)
	if got != "abc def" {
		t.Fatalf("unexpected sanitized content input: %q", got)
	}
	got = sanitizeQuickAddContentInput("123456789", 4)
	if got != "1234" {
		t.Fatalf("expected byte-limited content, got %q", got)
	}
}

func TestHandleUIQuickAddShareTargetRedirectsWithURLFallback(t *testing.T) {
	s := newAuthTestServer()
	form := "title=Example+Article&text=Read+https%3A%2F%2Fexample.com%2Fabc+later"
	req := httptest.NewRequest(http.MethodPost, "/ui/quick-add/share-target", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.handleUIQuickAddShareTarget(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("failed to parse redirect location %q: %v", loc, err)
	}
	if u.Path != "/ui/quick-add" {
		t.Fatalf("expected redirect path /ui/quick-add, got %q", u.Path)
	}
	if got := u.Query().Get("url"); got != "https://example.com/abc" {
		t.Fatalf("expected extracted url, got %q", got)
	}
	if got := u.Query().Get("title"); got != "Example Article" {
		t.Fatalf("expected title to be forwarded, got %q", got)
	}
	if got := u.Query().Get("content"); got != "Read later" {
		t.Fatalf("expected content preview, got %q", got)
	}
}

func TestCORSRejectsDisallowedOrigin(t *testing.T) {
	s := newAuthTestServer()
	s.cfg.CORSAllowOrigins = []string{"chrome-extension://allowed-extension-id"}

	called := false
	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://api.example.test/v1/items", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if called {
		t.Fatalf("expected next handler not to be called")
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin must not receive allow-origin header")
	}
}

func TestCORSAllowsConfiguredOriginPreflight(t *testing.T) {
	s := newAuthTestServer()
	allowed := "chrome-extension://allowed-extension-id"
	s.cfg.CORSAllowOrigins = []string{allowed}

	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodOptions, "https://api.example.test/v1/items", nil)
	req.Header.Set("Origin", allowed)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != allowed {
		t.Fatalf("expected allow-origin %q, got %q", allowed, rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

// CORS の Access-Control-Allow-Methods に PATCH が含まれることを検証する。
// PATCH /v1/items/{id}/status (Issue #119) を Chrome 拡張等のクロスオリジン
// クライアントから呼び出す際の preflight が成功する前提を守るための回帰テスト。
func TestCORSPreflightAllowsPATCHForStatusEndpoint(t *testing.T) {
	s := newAuthTestServer()
	allowed := "chrome-extension://allowed-extension-id"
	s.cfg.CORSAllowOrigins = []string{allowed}

	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodOptions, "https://api.example.test/v1/items/abc/status", nil)
	req.Header.Set("Origin", allowed)
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	methods := rr.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "PATCH") {
		t.Fatalf("expected Access-Control-Allow-Methods to include PATCH, got %q", methods)
	}
}

func TestCORSAllowsSameHostOrigin(t *testing.T) {
	s := newAuthTestServer()
	s.cfg.CORSAllowOrigins = nil

	called := false
	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://api.example.test/v1/items", nil)
	req.Header.Set("Origin", "https://api.example.test")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !called {
		t.Fatalf("expected next handler to be called")
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://api.example.test" {
		t.Fatalf("expected same-host origin to be allowed")
	}
}
