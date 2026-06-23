//go:build integration

package store

import (
	"context"
	"sort"
	"testing"
)

// TestTagsByNormalizedNames covers the helper used by the /ui/items fragment
// renderer (Issue #115) to resolve active filter chip display names without
// paying for the full ListTagsWithCountFiltered facet aggregate. Round-2 review
// of PR #137 flagged AC 1.3 (chips must show the original user-entered name)
// breaking on the fragment path when results are zero — this test guards the
// regression.
//
// Gated by -tags=integration and TEST_DATABASE_URL (see newIntegrationStore).
func TestTagsByNormalizedNames(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()

	// Seed: insert distinct tags whose normalized_name differs from name so the
	// regression assertion (chip shows "Go Lang", not "go lang") is meaningful.
	type seed struct{ name, normalized string }
	rows := []seed{
		{name: "Go Lang", normalized: "go lang"},
		{name: "Rust-Lang", normalized: "rust-lang"},
		{name: "TypeScript", normalized: "typescript"},
	}
	for _, r := range rows {
		if _, err := s.DB.Exec(ctx, `
			INSERT INTO tags (name, normalized_name) VALUES ($1, $2)
			ON CONFLICT (normalized_name) DO NOTHING
		`, r.name, r.normalized); err != nil {
			t.Fatalf("seed tag %q: %v", r.normalized, err)
		}
	}
	t.Cleanup(func() {
		for _, r := range rows {
			_, _ = s.DB.Exec(ctx, `DELETE FROM tags WHERE normalized_name = $1`, r.normalized)
		}
	})

	t.Run("empty input returns nil without query", func(t *testing.T) {
		got, err := s.TagsByNormalizedNames(ctx, nil)
		if err != nil {
			t.Fatalf("TagsByNormalizedNames(nil): %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %#v", got)
		}
	})

	t.Run("resolves display names for the requested set (AC 1.3 regression)", func(t *testing.T) {
		got, err := s.TagsByNormalizedNames(ctx, []string{"go lang", "rust-lang"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 tags, got %d: %#v", len(got), got)
		}
		// Order is not guaranteed by the query, normalize for assertion.
		sort.Slice(got, func(i, j int) bool { return got[i].NormalizedName < got[j].NormalizedName })
		if got[0].NormalizedName != "go lang" || got[0].Name != "Go Lang" {
			t.Errorf("expected (go lang, Go Lang), got (%q, %q)", got[0].NormalizedName, got[0].Name)
		}
		if got[1].NormalizedName != "rust-lang" || got[1].Name != "Rust-Lang" {
			t.Errorf("expected (rust-lang, Rust-Lang), got (%q, %q)", got[1].NormalizedName, got[1].Name)
		}
	})

	t.Run("unknown normalized names are silently absent", func(t *testing.T) {
		got, err := s.TagsByNormalizedNames(ctx, []string{"go lang", "does-not-exist"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 tag (unknown silently dropped), got %d: %#v", len(got), got)
		}
		if got[0].NormalizedName != "go lang" {
			t.Errorf("expected go lang, got %q", got[0].NormalizedName)
		}
	})
}
