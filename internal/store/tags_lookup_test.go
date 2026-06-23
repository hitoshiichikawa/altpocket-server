//go:build integration

package store

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTagsLookupStore opens a real Postgres connection for the user-scoped
// TagsByNormalizedNames test. Gated by TEST_DATABASE_URL so the default
// `go test ./...` invocation skips the integration path.
func newTagsLookupStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return &Store{DB: pool}, func() { pool.Close() }
}

// seedTagsLookupUser creates a throwaway user and returns its ID.
func seedTagsLookupUser(t *testing.T, s *Store, ctx context.Context, label string) string {
	t.Helper()
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO users (google_sub, email, name)
		VALUES ($1, $2, $3)
		RETURNING id
	`, "test-sub-"+t.Name()+"-"+label, t.Name()+"-"+label+"@example.invalid", t.Name()+"-"+label).Scan(&id)
	if err != nil {
		t.Fatalf("seed user %q: %v", label, err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// createUserItemWithDisplayTag exercises the real CreateItem path so the test
// matches production behavior. Round-5 review (Issue #115 / AC 1.3) flagged
// that direct-SQL seeding bypassed the save path, where display-name
// preservation now lives. CreateItem persists tags.name = display (NFKC + trim,
// case preserved) and tags.normalized_name = lowercase key as distinct values.
// The corresponding tag row is cleaned up after the test so concurrent runs
// don't leak a normalized_name row.
func createUserItemWithDisplayTag(t *testing.T, s *Store, ctx context.Context, userID, hash, displayTagName string) {
	t.Helper()
	display := strings.TrimSpace(displayTagName)
	normalized := strings.ToLower(display)
	inputs := []TagInput{{Name: display, NormalizedName: normalized}}
	rawURL := "https://example.invalid/" + hash
	if _, _, err := s.CreateItem(ctx, userID, rawURL, rawURL, hash, inputs, "title-"+hash, ""); err != nil {
		t.Fatalf("CreateItem %q: %v", hash, err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM tags WHERE normalized_name = $1`, normalized)
	})
}

// TestTagsByNormalizedNames covers the helper used by the /ui/items active
// filter chip rendering (Issue #115) to resolve display names without paying
// for the full ListTagsWithCountFiltered facet aggregate.
//
// Round-2 review flagged AC 1.3 (chips must show the original user-entered
// name) breaking on the fragment path when results are zero — this test guards
// that regression.
//
// Round-4 review flagged a multi-tenant isolation bug: the original query
// looked up tags globally by normalized_name, so a tag owned only by another
// user could leak its display name into the viewer's chip. The function now
// joins items / item_tags and filters by user_id; this test asserts that
// other-user tags are excluded.
//
// Gated by TEST_DATABASE_URL (see newTagsLookupStore).
func TestTagsByNormalizedNames(t *testing.T) {
	s, cleanup := newTagsLookupStore(t)
	defer cleanup()
	ctx := context.Background()

	viewer := seedTagsLookupUser(t, s, ctx, "viewer")
	other := seedTagsLookupUser(t, s, ctx, "other")

	// Items are now created via Store.CreateItem so the test exercises the
	// real save path and validates the round-5 display-name preservation
	// (Issue #115 / AC 1.3): tags.name keeps the user-entered casing while
	// tags.normalized_name is the lowercase key.
	createUserItemWithDisplayTag(t, s, ctx, viewer, "viewer-go", "Go Lang")
	createUserItemWithDisplayTag(t, s, ctx, viewer, "viewer-rust", "Rust-Lang")
	createUserItemWithDisplayTag(t, s, ctx, other, "other-ts", "TypeScript")

	t.Run("empty input returns nil without query", func(t *testing.T) {
		got, err := s.TagsByNormalizedNames(ctx, viewer, nil)
		if err != nil {
			t.Fatalf("TagsByNormalizedNames(nil): %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %#v", got)
		}
	})

	t.Run("resolves display names for the viewer's own tags (AC 1.3 regression)", func(t *testing.T) {
		got, err := s.TagsByNormalizedNames(ctx, viewer, []string{"go lang", "rust-lang"})
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
		got, err := s.TagsByNormalizedNames(ctx, viewer, []string{"go lang", "does-not-exist"})
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

	t.Run("Round-4 regression: tags owned only by another user are excluded", func(t *testing.T) {
		// The "typescript" tag exists globally (seeded above) but is linked
		// only to `other`'s item. Querying as `viewer` must drop it so the
		// viewer's chip can never inherit another user's display name.
		got, err := s.TagsByNormalizedNames(ctx, viewer, []string{"typescript"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected typescript to be filtered out for viewer, got %#v", got)
		}

		// And the same tag must still resolve for the user who actually owns
		// an item linked to it — guarding against an over-zealous filter that
		// would block legitimate lookups.
		gotOther, err := s.TagsByNormalizedNames(ctx, other, []string{"typescript"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames(other): %v", err)
		}
		if len(gotOther) != 1 || gotOther[0].Name != "TypeScript" {
			t.Fatalf("expected (TypeScript) for owning user, got %#v", gotOther)
		}
	})
}
