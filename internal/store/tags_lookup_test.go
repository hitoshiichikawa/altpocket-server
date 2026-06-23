//go:build integration

package store

import (
	"context"
	"os"
	"sort"
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

// seedDisplayTag inserts a tag whose display name differs from its normalized
// form (e.g. "Go Lang" / "go lang"). ON CONFLICT keeps the canonical display
// name even when a previous seed registered a different one.
func seedDisplayTag(t *testing.T, s *Store, ctx context.Context, name, normalized string) string {
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

// seedItemLinkedToTag inserts an item for userID and links it to tagID.
func seedItemLinkedToTag(t *testing.T, s *Store, ctx context.Context, userID, hash, tagID string) {
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
		t.Fatalf("link item_tag %q: %v", hash, err)
	}
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

	// "Go Lang" / "go lang" — owned by the viewer (via an item link).
	goTag := seedDisplayTag(t, s, ctx, "Go Lang", "go lang")
	seedItemLinkedToTag(t, s, ctx, viewer, "viewer-go", goTag)

	// "Rust-Lang" / "rust-lang" — owned by the viewer.
	rustTag := seedDisplayTag(t, s, ctx, "Rust-Lang", "rust-lang")
	seedItemLinkedToTag(t, s, ctx, viewer, "viewer-rust", rustTag)

	// "TypeScript" / "typescript" — owned only by `other`. The viewer must NOT
	// receive this tag back even when its normalized_name is requested.
	tsTag := seedDisplayTag(t, s, ctx, "TypeScript", "typescript")
	seedItemLinkedToTag(t, s, ctx, other, "other-ts", tsTag)

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
