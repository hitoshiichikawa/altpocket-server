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
// preservation now lives. CreateItem persists the per-user display name in
// item_tags.display_name (NFKC + trim, case preserved) and the shared
// tags.normalized_name = lowercase key; the global tags row is keyed only by
// normalized_name so it is shared across users (PR #137 codex [high] review —
// the display name is per-user). The corresponding tag row is cleaned up after
// the test so concurrent runs don't leak a normalized_name row.
func createUserItemWithDisplayTag(t *testing.T, s *Store, ctx context.Context, userID, hash, displayTagName string) {
	t.Helper()
	display := strings.TrimSpace(displayTagName)
	normalized := strings.ToLower(display)
	inputs := []TagInput{{Name: display, NormalizedName: normalized}}
	rawURL := "https://example.invalid/" + hash
	if _, _, err := s.CreateItem(ctx, userID, rawURL, rawURL, hash, inputs, "title-"+hash, ""); err != nil {
		t.Fatalf("CreateItem %q: %v", hash, err)
	}
	// Tags rows are user-independent (the global UNIQUE on normalized_name is
	// shared across users), so we must NOT issue an unguarded
	// `DELETE FROM tags WHERE normalized_name = $1` — that would cascade through
	// item_tags ON DELETE CASCADE and remove other users' / concurrent test
	// runs' item_tags rows. The `NOT EXISTS` guard keeps the deletion bounded
	// to orphan tags (no item_tags references remaining) so the cleanup is
	// safe even on a shared TEST_DATABASE_URL (round-6 of PR #137 / Issue
	// #115). Note LIFO of t.Cleanup means this runs BEFORE the user cascade
	// fires, so the guard typically falls through — an acceptable bounded
	// leak (orphan tags are reused by the next test that picks the same
	// normalized_name).
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, normalized)
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

// TestTagsByNormalizedNamesMultiTenantIsolation is the core regression for the
// PR #137 codex [high] finding (internal/store/store.go: shared tags.name leak).
// Two users tag items with the SAME normalized_name but DIFFERENT display names
// ("Go Lang" vs "GO LANG"). Each user's chip lookup must resolve ONLY that
// user's own display name; neither user's label may leak into the other's
// result (multi-tenant isolation / AC 1.3).
//
// Under the previous design the display name lived on the globally-shared tags
// row, so whichever user created the row first won the casing for everyone and
// the other user saw a foreign label. The per-user item_tags.display_name model
// fixes this; this test fails on the old design and passes on the new one.
//
// Gated by TEST_DATABASE_URL (see newTagsLookupStore).
func TestTagsByNormalizedNamesMultiTenantIsolation(t *testing.T) {
	s, cleanup := newTagsLookupStore(t)
	defer cleanup()
	ctx := context.Background()

	alice := seedTagsLookupUser(t, s, ctx, "alice")
	bob := seedTagsLookupUser(t, s, ctx, "bob")

	// Both users tag their own item with the same normalized "go lang" but
	// different display casing. createUserItemWithDisplayTag goes through the
	// real CreateItem save path, so this also exercises the conflict branch of
	// the shared tags upsert (the second CreateItem hits ON CONFLICT on the
	// shared normalized_name row).
	createUserItemWithDisplayTag(t, s, ctx, alice, "iso-alice-go", "Go Lang")
	createUserItemWithDisplayTag(t, s, ctx, bob, "iso-bob-go", "GO LANG")

	gotAlice, err := s.TagsByNormalizedNames(ctx, alice, []string{"go lang"})
	if err != nil {
		t.Fatalf("TagsByNormalizedNames(alice): %v", err)
	}
	if len(gotAlice) != 1 {
		t.Fatalf("alice: expected 1 tag, got %d: %#v", len(gotAlice), gotAlice)
	}
	if gotAlice[0].Name != "Go Lang" {
		t.Errorf("LEAK: alice sees display name %q, want her own %q (another user's casing leaked into her chip)", gotAlice[0].Name, "Go Lang")
	}

	gotBob, err := s.TagsByNormalizedNames(ctx, bob, []string{"go lang"})
	if err != nil {
		t.Fatalf("TagsByNormalizedNames(bob): %v", err)
	}
	if len(gotBob) != 1 {
		t.Fatalf("bob: expected 1 tag, got %d: %#v", len(gotBob), gotBob)
	}
	if gotBob[0].Name != "GO LANG" {
		t.Errorf("LEAK: bob sees display name %q, want his own %q (another user's casing leaked into his chip)", gotBob[0].Name, "GO LANG")
	}

	// Both resolutions must agree on the shared normalized identity (the global
	// filter key) even though the display names differ per user.
	if gotAlice[0].NormalizedName != "go lang" || gotBob[0].NormalizedName != "go lang" {
		t.Errorf("expected shared normalized_name %q for both, got alice=%q bob=%q", "go lang", gotAlice[0].NormalizedName, gotBob[0].NormalizedName)
	}
}

// TestTagsByNormalizedNamesAlsoSurfacesViaFacet asserts the sidebar facet path
// (ListTagsWithCountFiltered) is subject to the same per-user isolation as the
// direct chip lookup: two users sharing a normalized_name see their own display
// name in the facet, never the other's (PR #137 codex [high] — store.go:770
// previously returned the shared tags.name).
//
// Gated by TEST_DATABASE_URL.
func TestTagsByNormalizedNamesAlsoSurfacesViaFacet(t *testing.T) {
	s, cleanup := newTagsLookupStore(t)
	defer cleanup()
	ctx := context.Background()

	alice := seedTagsLookupUser(t, s, ctx, "facet-alice")
	bob := seedTagsLookupUser(t, s, ctx, "facet-bob")

	createUserItemWithDisplayTag(t, s, ctx, alice, "facet-alice-go", "Go Lang")
	createUserItemWithDisplayTag(t, s, ctx, bob, "facet-bob-go", "GO LANG")

	aliceFacet, err := s.ListTagsWithCountFiltered(ctx, alice, "", nil)
	if err != nil {
		t.Fatalf("ListTagsWithCountFiltered(alice): %v", err)
	}
	bobFacet, err := s.ListTagsWithCountFiltered(ctx, bob, "", nil)
	if err != nil {
		t.Fatalf("ListTagsWithCountFiltered(bob): %v", err)
	}

	findGo := func(tags []Tag) (Tag, bool) {
		for _, tg := range tags {
			if tg.NormalizedName == "go lang" {
				return tg, true
			}
		}
		return Tag{}, false
	}

	aGo, ok := findGo(aliceFacet)
	if !ok {
		t.Fatalf("alice facet missing 'go lang': %#v", aliceFacet)
	}
	if aGo.Name != "Go Lang" {
		t.Errorf("LEAK (facet): alice sees %q, want %q", aGo.Name, "Go Lang")
	}
	bGo, ok := findGo(bobFacet)
	if !ok {
		t.Fatalf("bob facet missing 'go lang': %#v", bobFacet)
	}
	if bGo.Name != "GO LANG" {
		t.Errorf("LEAK (facet): bob sees %q, want %q", bGo.Name, "GO LANG")
	}
}

// TestCreateAndPatchAgainstExistingSharedRow covers the PR #137 codex [high]
// conflict findings (store.go:338 / store.go:605): when a shared tags row for a
// normalized_name already exists (e.g. created earlier, possibly by another
// user), the save path must NOT silently drop the new display name, and an edit
// must reflect a casing change for the editing user.
//
// Gated by TEST_DATABASE_URL.
func TestCreateAndPatchAgainstExistingSharedRow(t *testing.T) {
	s, cleanup := newTagsLookupStore(t)
	defer cleanup()
	ctx := context.Background()

	first := seedTagsLookupUser(t, s, ctx, "shared-first")
	second := seedTagsLookupUser(t, s, ctx, "shared-second")

	// First user creates the shared tags row with a lowercase display name.
	createUserItemWithDisplayTag(t, s, ctx, first, "shared-first-go", "go lang")

	t.Run("Create against an existing shared row keeps the new user's display name", func(t *testing.T) {
		// Second user creates an item with the SAME normalized_name but a
		// distinct display name. The shared tags upsert hits ON CONFLICT, but
		// the display name lives in item_tags so it must be persisted intact.
		createUserItemWithDisplayTag(t, s, ctx, second, "shared-second-go", "Go LANG")

		got, err := s.TagsByNormalizedNames(ctx, second, []string{"go lang"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames(second): %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 tag, got %d: %#v", len(got), got)
		}
		if got[0].Name != "Go LANG" {
			t.Errorf("Create dropped the new display name: got %q, want %q (existing shared row must not win)", got[0].Name, "Go LANG")
		}

		// And the first user must be unaffected by the second user's create.
		gotFirst, err := s.TagsByNormalizedNames(ctx, first, []string{"go lang"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames(first): %v", err)
		}
		if len(gotFirst) != 1 || gotFirst[0].Name != "go lang" {
			t.Errorf("first user's display name changed after another user's create: got %#v, want %q", gotFirst, "go lang")
		}
	})

	t.Run("Patch reflects a casing change against an existing shared row", func(t *testing.T) {
		// Seed an item for a third actor with the lowercase tag, then edit it to
		// an upper-case display name. The shared tags row already exists, so the
		// old design's ON CONFLICT no-op would have frozen the label. The
		// per-item display_name must follow the edit.
		editor := seedTagsLookupUser(t, s, ctx, "shared-editor")
		hash := "shared-editor-go"
		rawURL := "https://example.invalid/" + hash
		var itemID string
		if err := s.DB.QueryRow(ctx, `
			INSERT INTO items (user_id, url, canonical_url, canonical_hash, title)
			VALUES ($1, $2, $2, $3, $4)
			RETURNING id
		`, editor, rawURL, hash, "editor-title").Scan(&itemID); err != nil {
			t.Fatalf("seed item: %v", err)
		}
		// Initial tag: lowercase.
		if _, err := s.ReplaceItemTags(ctx, editor, itemID, []TagInput{{Name: "go lang", NormalizedName: "go lang"}}); err != nil {
			t.Fatalf("ReplaceItemTags(initial): %v", err)
		}
		// Edit to a new casing.
		if _, err := s.ReplaceItemTags(ctx, editor, itemID, []TagInput{{Name: "GoLang!", NormalizedName: "go lang"}}); err != nil {
			t.Fatalf("ReplaceItemTags(edit): %v", err)
		}
		got, err := s.TagsByNormalizedNames(ctx, editor, []string{"go lang"})
		if err != nil {
			t.Fatalf("TagsByNormalizedNames(editor): %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 tag, got %d: %#v", len(got), got)
		}
		if got[0].Name != "GoLang!" {
			t.Errorf("Patch did not reflect the casing change: got %q, want %q", got[0].Name, "GoLang!")
		}
	})
}
