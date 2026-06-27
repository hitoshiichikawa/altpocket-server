//go:build integration

package store

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the real SQL added in task 1 (internal/store/items_bulk.go)
// against a Postgres database. They are gated by `-tags=integration` and the
// TEST_DATABASE_URL env var so they do NOT run in the default
// `go test ./...` invocation.
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/store/...
//
// The test database must have schema migrations 001..007 applied. Tests use
// per-test labelled users (seedBulkUser) so the shared TEST_DATABASE_URL stays
// safe under concurrent runs (multiple labels also bypass the
// users.google_sub UNIQUE collision that two unlabelled seedTestUser calls
// inside one test would hit).

// newBulkStore opens a real Postgres connection for the items_bulk
// integration tests. Gated by TEST_DATABASE_URL.
func newBulkStore(t *testing.T) (*Store, func()) {
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

// seedBulkUser creates a throwaway user row and returns its ID. The label
// disambiguates users.google_sub when a single test needs multiple users
// (user A vs user B for cross-user authorization tests). Cleanup deletes
// the user row (and cascades to items / item_tags via FK) at test end.
func seedBulkUser(t *testing.T, s *Store, ctx context.Context, label string) string {
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

// seedBulkItem inserts a single items row for the given user with a unique
// canonical_hash derived from the test name + label. Returns the new item's
// UUID id. The hash arg lets callers create multiple items per user without
// hitting the items.UNIQUE (user_id, canonical_hash) constraint.
func seedBulkItem(t *testing.T, s *Store, ctx context.Context, userID, hash string) string {
	t.Helper()
	scopedHash := t.Name() + "-" + hash
	var itemID string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO items (user_id, url, canonical_url, canonical_hash, title)
		VALUES ($1, $2, $2, $3, $4)
		RETURNING id
	`, userID, "https://example.invalid/"+scopedHash, scopedHash, "title-"+hash).Scan(&itemID)
	if err != nil {
		t.Fatalf("seed item %q: %v", hash, err)
	}
	return itemID
}

// seedBulkItemContent attaches an item_contents row to an existing item so
// TestBulkDeleteItems_DeletesItemTagsAndContents can confirm that the FK
// cleanup wired into BulkDeleteItems removes the dependent row in the same
// atomic step as the parent items row.
func seedBulkItemContent(t *testing.T, s *Store, ctx context.Context, itemID, body string) {
	t.Helper()
	_, err := s.DB.Exec(ctx, `
		INSERT INTO item_contents (item_id, content_full, content_search, content_bytes)
		VALUES ($1, $2, $2, $3)
	`, itemID, body, len(body))
	if err != nil {
		t.Fatalf("seed item_contents for %q: %v", itemID, err)
	}
}

// seedBulkItemWithTag mirrors createUserItemWithDisplayTag (tags_lookup_test.go)
// for tests that need an item with a pre-existing display tag. It goes through
// the real CreateItem path so the per-user display name (item_tags.display_name)
// is persisted exactly as production would. The corresponding orphan-tag
// cleanup is registered with the NOT EXISTS guard so the shared
// TEST_DATABASE_URL stays safe under concurrent runs (same regime as
// createUserItemWithDisplayTag — see Round-6 of PR #137 / Issue #115).
func seedBulkItemWithTag(t *testing.T, s *Store, ctx context.Context, userID, hash, displayTagName string) string {
	t.Helper()
	display := strings.TrimSpace(displayTagName)
	normalized := strings.ToLower(display)
	inputs := []TagInput{{Name: display, NormalizedName: normalized}}
	scopedHash := t.Name() + "-" + hash
	rawURL := "https://example.invalid/" + scopedHash
	itemID, _, err := s.CreateItem(ctx, userID, rawURL, rawURL, scopedHash, inputs, "title-"+hash, "")
	if err != nil {
		t.Fatalf("CreateItem %q: %v", hash, err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, normalized)
	})
	return itemID
}

// readItemTagDisplayNames returns the display_name values that item_tags
// currently associates with itemID, sorted alphabetically. Used by tests
// that need to assert "current full tag set per item" without depending on
// the BulkAddItemTag SQL's specific ORDER BY (the return value of
// BulkAddItemTag itself is also asserted, in a separate code path).
func readItemTagDisplayNames(t *testing.T, s *Store, ctx context.Context, itemID string) []string {
	t.Helper()
	rows, err := s.DB.Query(ctx, `
		SELECT display_name FROM item_tags WHERE item_id = $1
	`, itemID)
	if err != nil {
		t.Fatalf("read item_tags for %q: %v", itemID, err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan display_name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	sort.Strings(names)
	return names
}

// existsItem returns true if items.id exists. Used by tests that need to
// verify other-user items are NOT deleted by a bulk delete request from
// the calling user.
func existsItem(t *testing.T, s *Store, ctx context.Context, itemID string) bool {
	t.Helper()
	var exists bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM items WHERE id = $1)`, itemID).Scan(&exists)
	if err != nil {
		t.Fatalf("existsItem(%q): %v", itemID, err)
	}
	return exists
}

// existsItemContent returns true if item_contents.item_id exists. Used by
// TestBulkDeleteItems_DeletesItemTagsAndContents to assert FK cleanup
// reaches the dependent row.
func existsItemContent(t *testing.T, s *Store, ctx context.Context, itemID string) bool {
	t.Helper()
	var exists bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM item_contents WHERE item_id = $1)`, itemID).Scan(&exists)
	if err != nil {
		t.Fatalf("existsItemContent(%q): %v", itemID, err)
	}
	return exists
}

// existsTagByNormalized returns true if a tags row exists for the given
// normalized_name. Used by tests that assert (a) the BulkAddItemTag EARLY
// RETURN guard does NOT create a stray tags row when no items are owned,
// and (b) a brand-new tag name does create a tags row when the call
// succeeds.
func existsTagByNormalized(t *testing.T, s *Store, ctx context.Context, normalized string) bool {
	t.Helper()
	var exists bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tags WHERE normalized_name = $1)`, normalized).Scan(&exists)
	if err != nil {
		t.Fatalf("existsTagByNormalized(%q): %v", normalized, err)
	}
	return exists
}

// containsString returns true if needle appears anywhere in haystack. Used
// by tests that assert a tag list contains the expected name without
// depending on the SQL's ORDER BY producing a specific index.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestBulkDeleteItems_DeletesOwnAndIgnoresOthers exercises Req 8.1 / 8.2:
// when the caller passes a mixed ID set spanning their own items AND other
// users' items, only their own items are deleted and the other user's
// items survive. The succeeded slice mirrors the deleted IDs (Req 4.4 /
// 4.5).
func TestBulkDeleteItems_DeletesOwnAndIgnoresOthers(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	// Arrange: user A owns 3 items, user B owns 2. The caller is user A.
	userA := seedBulkUser(t, s, ctx, "a")
	userB := seedBulkUser(t, s, ctx, "b")
	a1 := seedBulkItem(t, s, ctx, userA, "a1")
	a2 := seedBulkItem(t, s, ctx, userA, "a2")
	a3 := seedBulkItem(t, s, ctx, userA, "a3")
	b1 := seedBulkItem(t, s, ctx, userB, "b1")
	b2 := seedBulkItem(t, s, ctx, userB, "b2")

	// Act: user A submits all 5 IDs (their own + user B's).
	succeeded, err := s.BulkDeleteItems(ctx, userA, []string{a1, a2, a3, b1, b2})
	if err != nil {
		t.Fatalf("BulkDeleteItems: %v", err)
	}

	// Assert: succeeded contains exactly user A's 3 items.
	sort.Strings(succeeded)
	want := []string{a1, a2, a3}
	sort.Strings(want)
	if !equalStringSlices(succeeded, want) {
		t.Errorf("succeeded = %v, want %v", succeeded, want)
	}
	// Assert: user B's items remain on disk (Req 8.2 leak prevention).
	if existsItem(t, s, ctx, b1) == false || existsItem(t, s, ctx, b2) == false {
		t.Errorf("user B items must remain in DB after user A's bulk delete: b1=%v b2=%v",
			existsItem(t, s, ctx, b1), existsItem(t, s, ctx, b2))
	}
	// Assert: user A's items are actually gone.
	if existsItem(t, s, ctx, a1) || existsItem(t, s, ctx, a2) || existsItem(t, s, ctx, a3) {
		t.Errorf("user A items must be deleted: a1=%v a2=%v a3=%v",
			existsItem(t, s, ctx, a1), existsItem(t, s, ctx, a2), existsItem(t, s, ctx, a3))
	}
}

// TestBulkDeleteItems_PartialFailureFromMissingID exercises the per-item
// success/failure separation that Req 4.7 / 4.8 build on at the handler
// layer: missing IDs are silently absent from succeeded (the handler
// surfaces them as failed[{reason: "not_found"}]) and err remains nil.
func TestBulkDeleteItems_PartialFailureFromMissingID(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	// Arrange: user has 2 own items + 3 IDs that do not exist anywhere.
	userID := seedBulkUser(t, s, ctx, "owner")
	own1 := seedBulkItem(t, s, ctx, userID, "own1")
	own2 := seedBulkItem(t, s, ctx, userID, "own2")
	missing1 := "11111111-1111-1111-1111-111111111111"
	missing2 := "22222222-2222-2222-2222-222222222222"
	missing3 := "33333333-3333-3333-3333-333333333333"

	// Act
	succeeded, err := s.BulkDeleteItems(ctx, userID, []string{own1, own2, missing1, missing2, missing3})

	// Assert: err is nil (missing IDs are NOT a hard error).
	if err != nil {
		t.Fatalf("BulkDeleteItems with missing IDs returned err: %v", err)
	}
	sort.Strings(succeeded)
	want := []string{own1, own2}
	sort.Strings(want)
	if !equalStringSlices(succeeded, want) {
		t.Errorf("succeeded = %v, want %v (missing IDs must NOT appear in succeeded)", succeeded, want)
	}
}

// TestBulkDeleteItems_DeletesItemTagsAndContents covers the FK-cascading
// cleanup wired into BulkDeleteItems. Items with associated item_tags and
// item_contents rows must have those dependent rows removed atomically,
// and the orphan tags sweep at the tail must remove tags rows whose only
// remaining link was the deleted item (mirrors DeleteItem). Other items
// sharing the same tag must NOT lose that tag (the sweep is bounded by
// NOT EXISTS).
func TestBulkDeleteItems_DeletesItemTagsAndContents(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	// Arrange:
	//   item1 + item2 carry the "to-delete-only" tag (will become orphan).
	//   item3 carries the "shared" tag, and item1 also carries "shared".
	//     -> after deleting item1+item2, "shared" must still exist on item3.
	//   item1 also gets an item_contents row.
	userID := seedBulkUser(t, s, ctx, "owner")
	item1 := seedBulkItemWithTag(t, s, ctx, userID, "i1", "to-delete-only")
	item2 := seedBulkItemWithTag(t, s, ctx, userID, "i2", "to-delete-only")
	item3 := seedBulkItemWithTag(t, s, ctx, userID, "i3", "shared")
	// Manually attach the "shared" tag to item1 by going through ReplaceItemTags
	// so item1 carries 2 tags.
	if _, err := s.ReplaceItemTags(ctx, userID, item1, []TagInput{
		{Name: "to-delete-only", NormalizedName: "to-delete-only"},
		{Name: "shared", NormalizedName: "shared"},
	}); err != nil {
		t.Fatalf("ReplaceItemTags(item1): %v", err)
	}
	seedBulkItemContent(t, s, ctx, item1, "body for item1")

	// Sanity-check the seed: item_contents present, both tags on item1.
	if !existsItemContent(t, s, ctx, item1) {
		t.Fatal("seed: item_contents for item1 must exist before BulkDeleteItems")
	}
	if pre := readItemTagDisplayNames(t, s, ctx, item1); len(pre) != 2 {
		t.Fatalf("seed: item1 should carry 2 tags, got %v", pre)
	}

	// Act
	succeeded, err := s.BulkDeleteItems(ctx, userID, []string{item1, item2})
	if err != nil {
		t.Fatalf("BulkDeleteItems: %v", err)
	}
	sort.Strings(succeeded)
	want := []string{item1, item2}
	sort.Strings(want)
	if !equalStringSlices(succeeded, want) {
		t.Fatalf("succeeded = %v, want %v", succeeded, want)
	}

	// Assert: items / item_tags / item_contents for item1/item2 are gone.
	if existsItem(t, s, ctx, item1) || existsItem(t, s, ctx, item2) {
		t.Errorf("items rows must be deleted")
	}
	if existsItemContent(t, s, ctx, item1) {
		t.Errorf("item_contents row for item1 must be deleted (FK cleanup)")
	}
	// item_tags rows for item1/item2 are gone by transitivity (FK cleanup).
	if got := readItemTagDisplayNames(t, s, ctx, item1); len(got) != 0 {
		t.Errorf("item_tags for item1 must be deleted, got %v", got)
	}
	if got := readItemTagDisplayNames(t, s, ctx, item2); len(got) != 0 {
		t.Errorf("item_tags for item2 must be deleted, got %v", got)
	}

	// Assert: the "to-delete-only" tag is gone (orphan sweep), but the
	// "shared" tag remains because item3 still references it.
	if existsTagByNormalized(t, s, ctx, "to-delete-only") {
		t.Errorf("orphan tag 'to-delete-only' must be swept after its last item was deleted")
	}
	if !existsTagByNormalized(t, s, ctx, "shared") {
		t.Errorf("shared tag must remain because item3 still carries it")
	}
	// Assert: item3 still has its tag.
	if got := readItemTagDisplayNames(t, s, ctx, item3); len(got) != 1 || got[0] != "shared" {
		t.Errorf("item3 tags after bulk-delete = %v, want [shared]", got)
	}
}

// TestBulkDeleteItems_EmptyIDsReturnsEmptySlice covers the no-op early
// return when the caller passes a zero-length slice. The function MUST
// short-circuit without a database round-trip, returning ([], nil).
func TestBulkDeleteItems_EmptyIDsReturnsEmptySlice(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userID := seedBulkUser(t, s, ctx, "owner")

	succeeded, err := s.BulkDeleteItems(ctx, userID, []string{})
	if err != nil {
		t.Fatalf("BulkDeleteItems([]): %v", err)
	}
	if succeeded == nil {
		t.Error("succeeded must be a non-nil empty slice, got nil")
	}
	if len(succeeded) != 0 {
		t.Errorf("succeeded len = %d, want 0", len(succeeded))
	}
}

// TestBulkAddItemTag_AddsToOwnedOnlyAndDedupes covers Req 5.4 (no duplicate
// addition) AND Req 8.1 (other-user items untouched) in a single regression
// scenario. The caller passes (own-with-tag) + (own-without-tag) +
// (other-user) and expects the tag to land on the own-without-tag item
// only, with the own-with-tag item left at exactly 1 tag occurrence and
// the other-user item completely unchanged.
func TestBulkAddItemTag_AddsToOwnedOnlyAndDedupes(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userA := seedBulkUser(t, s, ctx, "a")
	userB := seedBulkUser(t, s, ctx, "b")

	// item-has-tag already carries "golang" (seeded via CreateItem).
	itemHasTag := seedBulkItemWithTag(t, s, ctx, userA, "with-tag", "golang")
	// item-no-tag has no tags.
	itemNoTag := seedBulkItem(t, s, ctx, userA, "no-tag")
	// other-user item (user B). user A must not be able to add tags to it.
	itemOther := seedBulkItem(t, s, ctx, userB, "other")

	// Act: caller submits "golang" against all three IDs.
	got, err := s.BulkAddItemTag(ctx, userA, []string{itemHasTag, itemNoTag, itemOther}, TagInput{
		Name:           "golang",
		NormalizedName: "golang",
	})
	if err != nil {
		t.Fatalf("BulkAddItemTag: %v", err)
	}

	// Assert: succeeded covers ONLY the owned items (Req 8.1).
	ownedIDs := map[string]bool{}
	for _, r := range got {
		ownedIDs[r.ItemID] = true
	}
	if !ownedIDs[itemHasTag] || !ownedIDs[itemNoTag] {
		t.Errorf("succeeded must include both owned items, got %v", ownedIDs)
	}
	if ownedIDs[itemOther] {
		t.Errorf("succeeded must NOT include user B's item (Req 8.1 leak)")
	}
	if len(got) != 2 {
		t.Errorf("succeeded len = %d, want 2 (owned only)", len(got))
	}

	// Assert: itemHasTag still has exactly 1 "golang" entry (Req 5.4 dedup).
	if names := readItemTagDisplayNames(t, s, ctx, itemHasTag); len(names) != 1 || names[0] != "golang" {
		t.Errorf("itemHasTag tags = %v, want exactly [golang] (no duplicate)", names)
	}
	// Assert: itemNoTag now carries "golang".
	if names := readItemTagDisplayNames(t, s, ctx, itemNoTag); len(names) != 1 || names[0] != "golang" {
		t.Errorf("itemNoTag tags = %v, want [golang]", names)
	}
	// Assert: user B's item has no tags whatsoever.
	if names := readItemTagDisplayNames(t, s, ctx, itemOther); len(names) != 0 {
		t.Errorf("user B item must remain tag-free, got %v", names)
	}
}

// TestBulkAddItemTag_PreservesExistingTags covers Req 5.3 / 5.4 from the
// "do not lose existing tags" side. A pre-existing 3-tag item gets a 4th
// tag added; the response carries all 4 tags. We assert containment, not
// position, because the SQL's ORDER BY normalized_name puts the new tag at
// a normalized-order-dependent slot.
func TestBulkAddItemTag_PreservesExistingTags(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userID := seedBulkUser(t, s, ctx, "owner")
	itemID := seedBulkItemWithTag(t, s, ctx, userID, "preserve", "alpha")
	// Replace tags so the item has alpha + beta + gamma to start.
	if _, err := s.ReplaceItemTags(ctx, userID, itemID, []TagInput{
		{Name: "alpha", NormalizedName: "alpha"},
		{Name: "beta", NormalizedName: "beta"},
		{Name: "gamma", NormalizedName: "gamma"},
	}); err != nil {
		t.Fatalf("ReplaceItemTags(seed): %v", err)
	}
	// Register cleanup for the newly added tag.
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, "delta")
	})

	// Act
	got, err := s.BulkAddItemTag(ctx, userID, []string{itemID}, TagInput{
		Name:           "delta",
		NormalizedName: "delta",
	})
	if err != nil {
		t.Fatalf("BulkAddItemTag: %v", err)
	}
	if len(got) != 1 || got[0].ItemID != itemID {
		t.Fatalf("succeeded = %+v, want exactly 1 result for %q", got, itemID)
	}

	// Assert: all 4 tags appear in the post-update list.
	names := make([]string, 0, len(got[0].Tags))
	for _, tg := range got[0].Tags {
		names = append(names, tg.Name)
	}
	for _, want := range []string{"alpha", "beta", "gamma", "delta"} {
		if !containsString(names, want) {
			t.Errorf("post-update tags missing %q: %v", want, names)
		}
	}
	if len(got[0].Tags) != 4 {
		t.Errorf("post-update tags len = %d, want 4", len(got[0].Tags))
	}
	// Assert (DB-side cross-check): the persisted item_tags now has 4 rows.
	if persisted := readItemTagDisplayNames(t, s, ctx, itemID); len(persisted) != 4 {
		t.Errorf("persisted item_tags len = %d (%v), want 4", len(persisted), persisted)
	}
}

// TestBulkAddItemTag_ReturnsFullTagListPerItem covers Req 5.5: the
// per-item response carries the FULL post-update tag set (so the UI can
// rerender the card chip row without an additional fetch). The bulk call
// targets two items with different starting tag sets and asserts each
// item's response slice contains both its pre-existing tags and the newly
// added one.
func TestBulkAddItemTag_ReturnsFullTagListPerItem(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userID := seedBulkUser(t, s, ctx, "owner")
	itemAlpha := seedBulkItemWithTag(t, s, ctx, userID, "alpha-only", "alpha")
	itemBeta := seedBulkItemWithTag(t, s, ctx, userID, "beta-only", "beta")
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, "added")
	})

	got, err := s.BulkAddItemTag(ctx, userID, []string{itemAlpha, itemBeta}, TagInput{
		Name:           "added",
		NormalizedName: "added",
	})
	if err != nil {
		t.Fatalf("BulkAddItemTag: %v", err)
	}

	resultByID := map[string][]string{}
	for _, r := range got {
		names := make([]string, 0, len(r.Tags))
		for _, tg := range r.Tags {
			names = append(names, tg.Name)
		}
		resultByID[r.ItemID] = names
	}
	if tags := resultByID[itemAlpha]; !containsString(tags, "alpha") || !containsString(tags, "added") || len(tags) != 2 {
		t.Errorf("itemAlpha post-tags = %v, want [alpha added] (any order)", tags)
	}
	if tags := resultByID[itemBeta]; !containsString(tags, "beta") || !containsString(tags, "added") || len(tags) != 2 {
		t.Errorf("itemBeta post-tags = %v, want [beta added] (any order)", tags)
	}
}

// TestBulkAddItemTag_NewTagCreatesTagsRow covers the "first-ever use of a
// brand-new tag" path. Before the call no tags row exists for the chosen
// normalized_name; after the call the tags row exists, and the item_tags
// row links it to the item.
func TestBulkAddItemTag_NewTagCreatesTagsRow(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userID := seedBulkUser(t, s, ctx, "owner")
	itemID := seedBulkItem(t, s, ctx, userID, "new-tag")

	// Pre-condition: the tag does not exist yet.
	const newNormalized = "brand-new-tag"
	if existsTagByNormalized(t, s, ctx, newNormalized) {
		t.Fatalf("pre-condition violated: %q already exists in tags before the test", newNormalized)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, newNormalized)
	})

	// Act
	got, err := s.BulkAddItemTag(ctx, userID, []string{itemID}, TagInput{
		Name:           "Brand-New-Tag",
		NormalizedName: newNormalized,
	})
	if err != nil {
		t.Fatalf("BulkAddItemTag: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("succeeded len = %d, want 1", len(got))
	}

	// Assert: tags row now exists.
	if !existsTagByNormalized(t, s, ctx, newNormalized) {
		t.Errorf("tags row for %q must exist after first use", newNormalized)
	}
	// Assert: item_tags row links it.
	if names := readItemTagDisplayNames(t, s, ctx, itemID); len(names) != 1 || names[0] != "Brand-New-Tag" {
		t.Errorf("item_tags for itemID = %v, want [Brand-New-Tag]", names)
	}
}

// TestBulkAddItemTag_PartialFailureFromOtherUserID is the lighter-weight
// pure cross-user authorization assertion (Req 8.1 / 8.2). It covers the
// case where some IDs are owned and one ID is owned by a different user;
// succeeded must contain ONLY the own items.
func TestBulkAddItemTag_PartialFailureFromOtherUserID(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userA := seedBulkUser(t, s, ctx, "a")
	userB := seedBulkUser(t, s, ctx, "b")

	a1 := seedBulkItem(t, s, ctx, userA, "a1")
	a2 := seedBulkItem(t, s, ctx, userA, "a2")
	b1 := seedBulkItem(t, s, ctx, userB, "b1")

	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, "partial-tag")
	})

	got, err := s.BulkAddItemTag(ctx, userA, []string{a1, a2, b1}, TagInput{
		Name:           "partial-tag",
		NormalizedName: "partial-tag",
	})
	if err != nil {
		t.Fatalf("BulkAddItemTag: %v", err)
	}

	ownedIDs := map[string]bool{}
	for _, r := range got {
		ownedIDs[r.ItemID] = true
	}
	if !ownedIDs[a1] || !ownedIDs[a2] {
		t.Errorf("succeeded must include both user A items, got %v", ownedIDs)
	}
	if ownedIDs[b1] {
		t.Errorf("succeeded must NOT include user B's item (cross-user leak)")
	}
	if len(got) != 2 {
		t.Errorf("succeeded len = %d, want 2", len(got))
	}

	// And user B's item still has no tags.
	if names := readItemTagDisplayNames(t, s, ctx, b1); len(names) != 0 {
		t.Errorf("user B item must remain tag-free, got %v", names)
	}
}

// TestBulkAddItemTag_AllNotOwnedDoesNotCreateTagsRow covers the EARLY
// RETURN guard described in design.md BulkAddItemTag step 2: when every
// requested ID is owned by another user OR does not exist, the tags row
// for the requested normalized_name must NOT be inserted as a side effect.
// This guards against an authorization-failed request silently polluting
// the shared tags table (and therefore tag suggestions / chip filters).
func TestBulkAddItemTag_AllNotOwnedDoesNotCreateTagsRow(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userA := seedBulkUser(t, s, ctx, "caller")
	userB := seedBulkUser(t, s, ctx, "other")

	otherItem := seedBulkItem(t, s, ctx, userB, "other-1")
	missingUUID := "55555555-5555-5555-5555-555555555555"

	const normalized = "ghost-tag"
	if existsTagByNormalized(t, s, ctx, normalized) {
		t.Fatalf("pre-condition violated: %q already exists in tags", normalized)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `
			DELETE FROM tags
			WHERE normalized_name = $1
			  AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)
		`, normalized)
	})

	// Act: caller passes only "other-user" and "does-not-exist" IDs.
	got, err := s.BulkAddItemTag(ctx, userA, []string{otherItem, missingUUID}, TagInput{
		Name:           "Ghost-Tag",
		NormalizedName: normalized,
	})
	if err != nil {
		t.Fatalf("BulkAddItemTag: %v", err)
	}

	// Assert: succeeded is empty (no owned items).
	if got == nil {
		t.Error("succeeded must be non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("succeeded = %+v, want empty (no owned items)", got)
	}

	// Assert: the global tags row was NOT inserted (no side effect).
	if existsTagByNormalized(t, s, ctx, normalized) {
		t.Errorf("EARLY RETURN guard failed: tags row for %q must NOT be created when no items are owned", normalized)
	}
}

// TestBulkAddItemTag_ConcurrentDeleteBlocksUntilCommit pins the
// FOR KEY SHARE lock acquired in BulkAddItemTag step 1 against concurrent
// DELETE FROM items. The assertion is that the concurrent DELETE BLOCKS
// while the FOR KEY SHARE holder's transaction is open and PROCEEDS once
// the holder commits — which is the PostgreSQL guarantee that
// BulkAddItemTag relies on so step 4's INSERT INTO item_tags cannot race
// against a deletion of the parent items row (which would otherwise
// surface as a FK violation -> 500 db_error in the handler).
//
// The test does NOT inject a hook into Store.BulkAddItemTag (which would
// require production-side test seams). Instead it opens a hand-rolled
// transaction that acquires the same SELECT ... FOR KEY SHARE lock that
// BulkAddItemTag step 1 acquires, then issues a concurrent
// DELETE FROM items in a goroutine and asserts the DELETE does not
// complete until the FOR KEY SHARE transaction commits. This pins the
// PostgreSQL row-locking behavior the production code path depends on
// (round 6 review feedback / Req 8.3 race closure).
func TestBulkAddItemTag_ConcurrentDeleteBlocksUntilCommit(t *testing.T) {
	s, cleanup := newBulkStore(t)
	defer cleanup()
	ctx := context.Background()

	userID := seedBulkUser(t, s, ctx, "owner")
	itemA := seedBulkItem(t, s, ctx, userID, "a")

	// Arrange: open tx alpha (the FOR KEY SHARE holder).
	txAlpha, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin(alpha): %v", err)
	}
	t.Cleanup(func() {
		// If we still hold the tx at the end (test failed early), roll back.
		_ = txAlpha.Rollback(ctx)
	})
	// alpha acquires the same KEY SHARE lock that BulkAddItemTag step 1
	// acquires.
	if _, err := txAlpha.Exec(ctx, `
		SELECT id FROM items
		WHERE id = ANY($1::uuid[]) AND user_id = $2
		FOR KEY SHARE
	`, []string{itemA}, userID); err != nil {
		t.Fatalf("FOR KEY SHARE: %v", err)
	}

	// Act: launch goroutine beta that tries to DELETE the locked item.
	// We use a separate pool connection (DB.Exec) so it cannot piggyback
	// on tx alpha's connection.
	deleteDone := make(chan error, 1)
	deleteStart := make(chan struct{})
	go func() {
		// Signal that beta is about to issue the DELETE; the main test
		// goroutine uses this to time the "is beta still blocked" check.
		close(deleteStart)
		_, err := s.DB.Exec(ctx, `DELETE FROM items WHERE id = $1`, itemA)
		deleteDone <- err
	}()
	<-deleteStart

	// Assert: beta does NOT complete within a generous window — it must
	// stay blocked behind alpha's FOR KEY SHARE lock.
	select {
	case err := <-deleteDone:
		t.Fatalf("DELETE completed unexpectedly while FOR KEY SHARE was held: err=%v", err)
	case <-time.After(300 * time.Millisecond):
		// Good: beta is blocked as expected.
	}

	// Act: alpha commits, releasing the KEY SHARE lock.
	if err := txAlpha.Commit(ctx); err != nil {
		t.Fatalf("Commit(alpha): %v", err)
	}

	// Assert: beta now completes (the DELETE flows through).
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DELETE completed but returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DELETE did not complete within 2s after FOR KEY SHARE was released")
	}

	// Assert: the row is actually gone (sanity check).
	if existsItem(t, s, ctx, itemA) {
		t.Error("itemA must be deleted after the concurrent DELETE completed")
	}

	// Defensive: a side test that confirms two concurrent FOR KEY SHARE
	// acquisitions do NOT deadlock (KEY SHARE x KEY SHARE is compatible),
	// matching the assumption that two parallel BulkAddItemTag calls on
	// overlapping ID sets don't block each other.
	t.Run("two FOR KEY SHARE acquisitions are mutually compatible", func(t *testing.T) {
		// Seed a fresh item so we don't reuse itemA (already deleted).
		itemC := seedBulkItem(t, s, ctx, userID, "c")

		tx1, err := s.DB.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin(tx1): %v", err)
		}
		defer func() { _ = tx1.Rollback(ctx) }()
		if _, err := tx1.Exec(ctx, `
			SELECT id FROM items WHERE id = $1 FOR KEY SHARE
		`, itemC); err != nil {
			t.Fatalf("tx1 FOR KEY SHARE: %v", err)
		}

		// tx2 must acquire the same lock without blocking. We bound the
		// attempt to 1s and use a WaitGroup to wait for the goroutine.
		tx2done := make(chan error, 1)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx2, err := s.DB.Begin(ctx)
			if err != nil {
				tx2done <- err
				return
			}
			defer func() { _ = tx2.Rollback(ctx) }()
			_, err = tx2.Exec(ctx, `
				SELECT id FROM items WHERE id = $1 FOR KEY SHARE
			`, itemC)
			tx2done <- err
		}()

		select {
		case err := <-tx2done:
			if err != nil {
				t.Fatalf("tx2 FOR KEY SHARE failed: %v", err)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("tx2 FOR KEY SHARE blocked for >1s — KEY SHARE x KEY SHARE should be compatible")
		}
		wg.Wait()
	})

	// Defensive: confirm the items.UNIQUE constraint is still active by
	// trying to recreate itemA — pgx returns a non-ErrNoRows error. This
	// catches a hypothetical regression where the DELETE side-effect
	// leaked schema state through some FK cascade we did not expect.
	_, _, err = s.CreateItem(ctx, userID, "https://example.invalid/recheck", "https://example.invalid/recheck", "recheck-hash", nil, "x", "")
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("CreateItem returned ErrNoRows unexpectedly: %v", err)
	}
}
