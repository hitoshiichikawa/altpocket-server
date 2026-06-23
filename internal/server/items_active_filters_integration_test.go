//go:build integration

package server

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"altpocket/internal/store"
)

// These tests exercise the real SQL against a Postgres database. They are
// gated by `-tags=integration` and the TEST_DATABASE_URL env var so they do
// not run in default unit-test invocations.
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/server/...
//
// The test database must have schema migrations 001..004 applied.

func newIntegrationStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return &store.Store{DB: pool}, func() { pool.Close() }
}

// seedItemsActiveFilterUser creates a throwaway user and returns its ID.
func seedItemsActiveFilterUser(t *testing.T, s *store.Store, ctx context.Context) string {
	t.Helper()
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO users (google_sub, email, name)
		VALUES ($1, $2, $3)
		RETURNING id
	`, "test-sub-"+t.Name(), t.Name()+"@example.invalid", t.Name()).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// createItemWithDisplayTag exercises the real save path (Store.CreateItem) so
// that the regression assertions reflect production behavior. Round-5 review
// (Issue #115 / AC 1.3) flagged that the previous direct-SQL seeding bypassed
// the save path, where display-name preservation now lives. The store now
// persists tags.name = display (NFKC + trim, case preserved) and
// tags.normalized_name = lowercase key, so passing "Go Lang" here results in
// the chip rendering "Go Lang" rather than the normalized "go lang".
func createItemWithDisplayTag(t *testing.T, s *store.Store, ctx context.Context, userID, hash, displayTagName string) {
	t.Helper()
	// Build TagInput pairs the same way the HTTP handlers do — via
	// normalizeTagInputs — so the end-to-end behavior matches handleCreateItem.
	tagInputs := normalizeTagInputs([]string{displayTagName})
	rawURL := "https://example.invalid/" + hash
	if _, _, err := s.CreateItem(ctx, userID, rawURL, rawURL, hash, tagInputs, "title-"+hash, ""); err != nil {
		t.Fatalf("CreateItem %q: %v", hash, err)
	}
	// Item cleanup happens via the user cascade in seedItemsActiveFilterUser's
	// t.Cleanup, but tags are user-independent and need explicit removal so
	// concurrent test runs cannot leak normalized_name rows.
	normalized := tagInputs[0].NormalizedName
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM tags WHERE normalized_name = $1`, normalized)
	})
}

// TestHandleUIItemsFullPageZeroResultResolvesDisplayName guards the round-3
// review regression (Issue #115, PR #137): on the full-page render path a tag
// AND-condition that yields zero items leaves the filtered facet
// (ListTagsWithCountFiltered) empty, so the active filter chips degraded to the
// normalized lowercase form. This violated AC 1.3 (docs/specs/115-issue/
// requirements.md:30 — chips must show the original display name) and AC 4.5
// (requirements.md:70 — direct URL open must match the query).
//
// The handler now merges the empty facet with a direct TagsByNormalizedNames
// lookup (mergeTagDisplaySources) so the chips resolve the user-entered display
// name even for zero-result filters. This test reproduces the handler's
// full-page data path against the real database and asserts the chips show the
// original display name rather than the normalized form.
//
// Round-5 update: items are now created via Store.CreateItem instead of
// direct SQL so the test also covers the save path (round-5 reviewer's
// concern that the previous fixture bypassed it).
func TestHandleUIItemsFullPageZeroResultResolvesDisplayName(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedItemsActiveFilterUser(t, s, ctx)

	// Two tags with distinct display names. Each tag is on a *separate* item,
	// so the AND of both ("go lang" AND "rust lang") matches zero items — the
	// exact condition that emptied the facet and triggered the bug. The save
	// path itself now preserves "Go Lang" / "Rust Lang" as tags.name.
	createItemWithDisplayTag(t, s, ctx, userID, "item-go", "Go Lang")
	createItemWithDisplayTag(t, s, ctx, userID, "item-rust", "Rust Lang")

	// Active filters as parsed from ?tag=go+lang&tag=rust+lang on a full-page
	// (non-fragment) request.
	tagFilters := []string{"go lang", "rust lang"}

	// Reproduce the handler's full-page data path (handleUIItems, fragmentOnly=false).
	items, _, err := s.ListItems(ctx, userID, 1, 20, "", tagFilters, "newest")
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected zero items for the go+rust AND filter, got %d", len(items))
	}

	facet, err := s.ListTagsWithCountFiltered(ctx, userID, "", tagFilters)
	if err != nil {
		t.Fatalf("ListTagsWithCountFiltered: %v", err)
	}
	// Precondition that makes this regression meaningful: the facet is empty
	// for the zero-result filter, so the old code would have fallen back to
	// the normalized name. If this ever becomes non-empty the test no longer
	// guards the intended path.
	if len(facet) != 0 {
		t.Fatalf("expected empty facet for zero-result filter, got %d entries: %#v", len(facet), facet)
	}

	named, err := s.TagsByNormalizedNames(ctx, userID, tagFilters)
	if err != nil {
		t.Fatalf("TagsByNormalizedNames: %v", err)
	}
	tagsForLookup := mergeTagDisplaySources(facet, named)

	currentURL, _ := url.Parse("/ui/items?tag=go+lang&tag=rust+lang")
	chips := buildActiveTagFilters(tagFilters, tagsForLookup, items, currentURL)

	if len(chips) != 2 {
		t.Fatalf("expected 2 chips, got %d: %#v", len(chips), chips)
	}
	byNorm := map[string]string{}
	for _, c := range chips {
		byNorm[c.NormalizedName] = c.Name
	}
	if got := byNorm["go lang"]; got != "Go Lang" {
		t.Errorf("chip for go lang = %q, want original display name %q (regression to normalized form)", got, "Go Lang")
	}
	if got := byNorm["rust lang"]; got != "Rust Lang" {
		t.Errorf("chip for rust lang = %q, want original display name %q (regression to normalized form)", got, "Rust Lang")
	}
}

// TestSaveAndEditPathPreservesDisplayName guards the round-5 review regression
// (Issue #115 / AC 1.3): the previous code path normalized tag names to
// lowercase before storing, so even when the user entered "Go Lang" the chip
// rendered "go lang" — violating AC 1.3 (chips must show the original display
// name). The save path now persists tags.name = display (NFKC + trim, case
// preserved) and tags.normalized_name = lowercase key as distinct values, so
// both Create and Patch flows surface the user-entered casing.
//
// Round-4 reviewer's `TagsByNormalizedNames(userID, ...)` is intentionally
// reused here so the test covers Create + Edit + display-name lookup in one
// end-to-end path through the real database.
func TestSaveAndEditPathPreservesDisplayName(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedItemsActiveFilterUser(t, s, ctx)

	t.Run("CreateItem preserves user-entered casing", func(t *testing.T) {
		hash := "create-path-go"
		rawURL := "https://example.invalid/" + hash
		inputs := normalizeTagInputs([]string{"Go Lang"})
		if _, _, err := s.CreateItem(ctx, userID, rawURL, rawURL, hash, inputs, "create-title", ""); err != nil {
			t.Fatalf("CreateItem: %v", err)
		}
		t.Cleanup(func() {
			_, _ = s.DB.Exec(ctx, `DELETE FROM tags WHERE normalized_name = $1`, "go lang")
		})

		got, err := s.TagsByNormalizedNames(ctx, userID, []string{"go lang"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 tag, got %d: %#v", len(got), got)
		}
		if got[0].Name != "Go Lang" {
			t.Errorf("tags.name = %q, want %q (round-5 regression — save path must keep original casing)", got[0].Name, "Go Lang")
		}
		if got[0].NormalizedName != "go lang" {
			t.Errorf("tags.normalized_name = %q, want %q", got[0].NormalizedName, "go lang")
		}
	})

	t.Run("PatchItem preserves user-entered casing on edit", func(t *testing.T) {
		// Seed a fresh item with normalized tag, then edit it to a display
		// name with distinct casing to verify the edit path also preserves
		// the user-entered form.
		hash := "patch-path-rust"
		rawURL := "https://example.invalid/" + hash
		var itemID string
		err := s.DB.QueryRow(ctx, `
			INSERT INTO items (user_id, url, canonical_url, canonical_hash, title)
			VALUES ($1, $2, $2, $3, $4)
			RETURNING id
		`, userID, rawURL, hash, "patch-title").Scan(&itemID)
		if err != nil {
			t.Fatalf("seed item: %v", err)
		}

		newTags := normalizeTagInputs([]string{"Rust-Lang"})
		if _, _, err := s.PatchItem(ctx, userID, itemID, nil, &newTags); err != nil {
			t.Fatalf("PatchItem: %v", err)
		}
		t.Cleanup(func() {
			_, _ = s.DB.Exec(ctx, `DELETE FROM tags WHERE normalized_name = $1`, "rust-lang")
		})

		got, err := s.TagsByNormalizedNames(ctx, userID, []string{"rust-lang"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 tag, got %d: %#v", len(got), got)
		}
		if got[0].Name != "Rust-Lang" {
			t.Errorf("tags.name = %q, want %q (round-5 regression — patch path must keep original casing)", got[0].Name, "Rust-Lang")
		}
	})

	t.Run("CreateItem with NFKC fullwidth input folds form while preserving case", func(t *testing.T) {
		// NFKC normalizes fullwidth letters to halfwidth, but case is preserved
		// in the display name. AC 1.3 says "正規化前の元の表示名" — Unicode
		// folding (NFKC) is the standard interpretation of "normalization", and
		// the case-preserving form is what the user actually sees.
		hash := "create-nfkc"
		rawURL := "https://example.invalid/" + hash
		inputs := normalizeTagInputs([]string{"ＧｏＬａｎｇ"})
		if _, _, err := s.CreateItem(ctx, userID, rawURL, rawURL, hash, inputs, "nfkc-title", ""); err != nil {
			t.Fatalf("CreateItem: %v", err)
		}
		t.Cleanup(func() {
			_, _ = s.DB.Exec(ctx, `DELETE FROM tags WHERE normalized_name = $1`, "golang")
		})

		got, err := s.TagsByNormalizedNames(ctx, userID, []string{"golang"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 tag, got %d: %#v", len(got), got)
		}
		if got[0].Name != "GoLang" {
			t.Errorf("tags.name = %q, want %q (NFKC fold + case preserved)", got[0].Name, "GoLang")
		}
		if got[0].NormalizedName != "golang" {
			t.Errorf("tags.normalized_name = %q, want %q", got[0].NormalizedName, "golang")
		}
	})
}
