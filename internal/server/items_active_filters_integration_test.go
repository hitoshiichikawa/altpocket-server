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

// seedDisplayNameTag inserts a tag whose display name differs from its
// normalized form (e.g. "Go Lang" / "go lang") so that the chip regression
// assertion (chip must show "Go Lang", not "go lang") is meaningful. Direct
// SQL is used rather than CreateItem because CreateItem stores tags with
// name == normalized_name (ON CONFLICT DO UPDATE SET name=EXCLUDED.name),
// which would erase the distinct display name.
func seedDisplayNameTag(t *testing.T, s *store.Store, ctx context.Context, name, normalized string) string {
	t.Helper()
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO tags (name, normalized_name)
		VALUES ($1, $2)
		ON CONFLICT (normalized_name) DO UPDATE SET name=EXCLUDED.name
		RETURNING id
	`, name, normalized).Scan(&id)
	if err != nil {
		t.Fatalf("seed tag %q: %v", normalized, err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM tags WHERE normalized_name = $1`, normalized)
	})
	return id
}

// seedItemWithTag inserts an item for the user and links it to a single tag.
func seedItemWithTag(t *testing.T, s *store.Store, ctx context.Context, userID, hash, tagID string) {
	t.Helper()
	var itemID string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO items (user_id, url, canonical_url, canonical_hash, title)
		VALUES ($1, $2, $2, $3, $4)
		RETURNING id
	`, userID, "https://example.invalid/"+hash, hash, "title-"+hash).Scan(&itemID)
	if err != nil {
		t.Fatalf("seed item %q: %v", hash, err)
	}
	if _, err := s.DB.Exec(ctx, `
		INSERT INTO item_tags (item_id, tag_id) VALUES ($1, $2)
	`, itemID, tagID); err != nil {
		t.Fatalf("link item_tag: %v", err)
	}
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
func TestHandleUIItemsFullPageZeroResultResolvesDisplayName(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedItemsActiveFilterUser(t, s, ctx)

	// Two tags with distinct display names. Each tag is on a *separate* item,
	// so the AND of both ("go lang" AND "rust lang") matches zero items — the
	// exact condition that emptied the facet and triggered the bug.
	goTagID := seedDisplayNameTag(t, s, ctx, "Go Lang", "go lang")
	rustTagID := seedDisplayNameTag(t, s, ctx, "Rust Lang", "rust lang")
	seedItemWithTag(t, s, ctx, userID, "item-go", goTagID)
	seedItemWithTag(t, s, ctx, userID, "item-rust", rustTagID)

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
