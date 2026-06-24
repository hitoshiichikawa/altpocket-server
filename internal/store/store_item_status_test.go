//go:build integration

package store

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// These tests exercise the real SQL against a Postgres database. They are
// gated by `-tags=integration` and the TEST_DATABASE_URL env var so they do
// not run in default unit-test invocations.
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/store/...
//
// The test database must have schema migrations 001..007 applied
// (007 adds items.status TEXT NOT NULL DEFAULT 'unread' + CHECK constraint
// + items_user_status_idx index).
//
// All tests reuse the package-local newIntegrationStore / seedTestUser
// helpers defined in mcp_api_key_test.go (same package, same build tag).

// seedItemForStatusTest inserts a single items row for the given user with the
// requested initial status and fetch_status. status is set explicitly so each
// test can start from a deterministic state regardless of the items.status
// DEFAULT ('unread'). canonicalHash is unique-scoped to the user via the
// items.UNIQUE (user_id, canonical_hash) constraint, so callers can construct
// a stable identifier from the test name.
func seedItemForStatusTest(t *testing.T, s *Store, ctx context.Context, userID, canonicalHash, status, fetchStatus string) string {
	t.Helper()
	var itemID string
	rawURL := "https://example.invalid/" + canonicalHash
	err := s.DB.QueryRow(ctx, `
		INSERT INTO items (user_id, url, canonical_url, canonical_hash, title, fetch_status, status)
		VALUES ($1, $2, $2, $3, $4, $5, $6)
		RETURNING id
	`, userID, rawURL, canonicalHash, "title-"+canonicalHash, fetchStatus, status).Scan(&itemID)
	if err != nil {
		t.Fatalf("seed item %q (status=%s fetch_status=%s): %v", canonicalHash, status, fetchStatus, err)
	}
	return itemID
}

// readRowStatusAndFetchStatus reads the current (status, fetch_status) pair
// from items by id. Used by tests to verify axis independence (Req 1.6).
func readRowStatusAndFetchStatus(t *testing.T, s *Store, ctx context.Context, itemID string) (string, string) {
	t.Helper()
	var status, fetchStatus string
	if err := s.DB.QueryRow(ctx, `
		SELECT status, fetch_status FROM items WHERE id = $1
	`, itemID).Scan(&status, &fetchStatus); err != nil {
		t.Fatalf("read item %q: %v", itemID, err)
	}
	return status, fetchStatus
}

// TestUpdateItemStatus_TransitionsAllPairs verifies the 7 transition pairs
// declared in design.md's State Transitions diagram, plus the same-value
// "reset" case, and confirms the prev return value reflects the pre-update
// value (NFR 3.1 / Req 1.4).
func TestUpdateItemStatus_TransitionsAllPairs(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	cases := []struct {
		name string
		from string
		to   string
	}{
		{"unread_to_read", ItemStatusUnread, ItemStatusRead},
		{"read_to_unread", ItemStatusRead, ItemStatusUnread},
		{"unread_to_archived", ItemStatusUnread, ItemStatusArchived},
		{"read_to_archived", ItemStatusRead, ItemStatusArchived},
		{"archived_to_unread", ItemStatusArchived, ItemStatusUnread},
		{"archived_to_read", ItemStatusArchived, ItemStatusRead},
		{"same_value_unread", ItemStatusUnread, ItemStatusUnread},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: seed an item with the source status. A unique hash per
			// case keeps the rows independent so concurrent / ordered tests do
			// not collide on items.UNIQUE (user_id, canonical_hash).
			itemID := seedItemForStatusTest(t, s, ctx, userID, "transition-"+tc.name+"-"+string(rune('a'+i)), tc.from, "success")

			// Act
			prev, err := s.UpdateItemStatus(ctx, userID, itemID, tc.to)
			if err != nil {
				t.Fatalf("UpdateItemStatus(%s -> %s): %v", tc.from, tc.to, err)
			}

			// Assert: prev MUST be the pre-update value (the CTE-based SQL in
			// store.go captures this via `prev` CTE + FOR UPDATE).
			if prev != tc.from {
				t.Errorf("prev = %q, want %q (pre-update value)", prev, tc.from)
			}

			// Assert: the persisted row reflects the new status.
			gotStatus, _ := readRowStatusAndFetchStatus(t, s, ctx, itemID)
			if gotStatus != tc.to {
				t.Errorf("persisted status = %q, want %q", gotStatus, tc.to)
			}
		})
	}
}

// TestUpdateItemStatus_RejectsOtherUserItem covers NFR 2.1: a user cannot
// update another user's item. The collision between "not found" and
// "owned by another user" is intentionally collapsed into pgx.ErrNoRows
// (design.md Error Handling / store.UpdateItemStatus doc comment).
func TestUpdateItemStatus_RejectsOtherUserItem(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()

	// Arrange: two distinct users; user A owns the item, user B will try.
	userA := seedTestUser(t, s)
	userB := seedTestUser(t, s)
	itemID := seedItemForStatusTest(t, s, ctx, userA, "cross-user", ItemStatusUnread, "success")

	// Act: user B attempts to flip the item to "read".
	_, err := s.UpdateItemStatus(ctx, userB, itemID, ItemStatusRead)

	// Assert: ErrNoRows, and the underlying row is not mutated.
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows from cross-user update, got %v", err)
	}
	gotStatus, _ := readRowStatusAndFetchStatus(t, s, ctx, itemID)
	if gotStatus != ItemStatusUnread {
		t.Errorf("status after rejected update = %q, want %q (must remain unchanged)", gotStatus, ItemStatusUnread)
	}
}

// TestUpdateItemStatus_RejectsInvalidStatus covers Req 1.5: the DB-level
// CHECK constraint (items_status_check) rejects any value outside the
// canonical 3-value enum. This is the defense-in-depth layer behind the
// API enum validation in handleSetItemStatus (task 4).
func TestUpdateItemStatus_RejectsInvalidStatus(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)
	itemID := seedItemForStatusTest(t, s, ctx, userID, "invalid-status", ItemStatusUnread, "success")

	// Act
	_, err := s.UpdateItemStatus(ctx, userID, itemID, "bogus_value")

	// Assert: the CHECK constraint surfaces as a non-nil error. We do not
	// inspect the PG error code beyond confirming the call failed so the
	// test stays resilient to driver-level error wrapping changes.
	if err == nil {
		t.Fatal("expected CHECK constraint violation for bogus status, got nil")
	}
	// pgx.ErrNoRows would indicate ownership-check failure, NOT a CHECK
	// violation — guard against that masking the real signal.
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unexpected pgx.ErrNoRows (should be CHECK violation): %v", err)
	}
	// And the underlying row is untouched.
	gotStatus, _ := readRowStatusAndFetchStatus(t, s, ctx, itemID)
	if gotStatus != ItemStatusUnread {
		t.Errorf("status after rejected update = %q, want %q (must remain unchanged)", gotStatus, ItemStatusUnread)
	}
}

// collectItemIDs collects ItemListRow.ID values into a sorted []string so
// test cases can assert membership independent of result ordering.
func collectItemIDs(rows []ItemListRow) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return ids
}

// TestListItems_FilterByStatus covers Req 3.3 / 3.4 / 3.5 / 6.2: the
// statuses slice constrains the returned rows when non-empty, and nil / empty
// returns all states (the "no filter" sentinel).
func TestListItems_FilterByStatus(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	unreadID := seedItemForStatusTest(t, s, ctx, userID, "list-unread", ItemStatusUnread, "success")
	readID := seedItemForStatusTest(t, s, ctx, userID, "list-read", ItemStatusRead, "success")
	archivedID := seedItemForStatusTest(t, s, ctx, userID, "list-archived", ItemStatusArchived, "success")

	cases := []struct {
		name     string
		statuses []string
		wantIDs  []string
	}{
		{"nil_returns_all", nil, []string{unreadID, readID, archivedID}},
		{"empty_returns_all", []string{}, []string{unreadID, readID, archivedID}},
		{"unread_only", []string{ItemStatusUnread}, []string{unreadID}},
		{"unread_and_read_all_tab", []string{ItemStatusUnread, ItemStatusRead}, []string{unreadID, readID}},
		{"archived_only", []string{ItemStatusArchived}, []string{archivedID}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := s.ListItems(ctx, userID, 1, 50, "", nil, tc.statuses, "newest")
			if err != nil {
				t.Fatalf("ListItems: %v", err)
			}
			got := collectItemIDs(rows)
			want := append([]string(nil), tc.wantIDs...)
			sort.Strings(want)
			if !equalStringSlices(got, want) {
				t.Errorf("ids = %v, want %v", got, want)
			}
		})
	}
}

// TestListRecentItems_FilterByStatus mirrors the ListItems coverage for the
// MCP `recent-articles` / Tool list_items path (Req 5.3 / 6.2). Since the
// recentArticlesHandler always passes nil (Resource has no input args), the
// nil case is the most-trafficked path in production.
func TestListRecentItems_FilterByStatus(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	unreadID := seedItemForStatusTest(t, s, ctx, userID, "recent-unread", ItemStatusUnread, "success")
	readID := seedItemForStatusTest(t, s, ctx, userID, "recent-read", ItemStatusRead, "success")
	archivedID := seedItemForStatusTest(t, s, ctx, userID, "recent-archived", ItemStatusArchived, "success")

	// since: 1 hour in the past — well before the rows were just-inserted, so
	// all three are eligible.
	since := time.Now().Add(-1 * time.Hour)

	cases := []struct {
		name     string
		statuses []string
		wantIDs  []string
	}{
		{"nil_returns_all", nil, []string{unreadID, readID, archivedID}},
		{"empty_returns_all", []string{}, []string{unreadID, readID, archivedID}},
		{"unread_only", []string{ItemStatusUnread}, []string{unreadID}},
		{"unread_and_read_all_tab", []string{ItemStatusUnread, ItemStatusRead}, []string{unreadID, readID}},
		{"archived_only", []string{ItemStatusArchived}, []string{archivedID}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := s.ListRecentItems(ctx, userID, since, tc.statuses)
			if err != nil {
				t.Fatalf("ListRecentItems: %v", err)
			}
			got := collectItemIDs(rows)
			want := append([]string(nil), tc.wantIDs...)
			sort.Strings(want)
			if !equalStringSlices(got, want) {
				t.Errorf("ids = %v, want %v", got, want)
			}
		})
	}
}

// TestMigration007_BackfillsExistingItemsToUnread covers Req 1.3 / 6.1: the
// migration applies `ADD COLUMN status TEXT NOT NULL DEFAULT 'unread'`,
// which PostgreSQL 11+ resolves with the metadata-default fast path so
// existing rows surface as 'unread' without rewriting. The CHECK constraint
// must also be active after the migration.
//
// To regression-fix the migration's backfill behavior without rewriting the
// production schema, the test runs entirely inside a transaction and uses
// PostgreSQL's transactional DDL to:
//
//  1. DROP CONSTRAINT items_status_check / DROP COLUMN status — re-creates the
//     pre-007 schema state in this transaction's snapshot.
//  2. INSERT items rows that have no status column (simulating pre-007 data).
//  3. Re-apply the 007 migration's three statements (ADD COLUMN + DO $$
//     ALTER ADD CONSTRAINT + CREATE INDEX) verbatim.
//  4. Assert all rows backfilled to 'unread' and the CHECK constraint
//     rejects out-of-range values.
//
// The transaction is rolled back at the end so the production schema is
// untouched. The shared TEST_DATABASE_URL stays safe even on concurrent runs
// because PostgreSQL DDL inside a transaction is isolated from other
// connections.
func TestMigration007_BackfillsExistingItemsToUnread(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() {
		// Always rollback so the production schema is restored. Errors are
		// intentionally ignored — the test has already asserted what it needs.
		_ = tx.Rollback(ctx)
	}()

	// Seed a test user inside the same transaction so the user row is also
	// rolled back and does not leak.
	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (google_sub, email, name)
		VALUES ($1, $2, $3)
		RETURNING id
	`, "test-sub-"+t.Name(), t.Name()+"@example.invalid", t.Name()).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Step 1: roll the schema back to the pre-007 shape inside this tx.
	if _, err := tx.Exec(ctx, `ALTER TABLE items DROP CONSTRAINT IF EXISTS items_status_check`); err != nil {
		t.Fatalf("drop constraint: %v", err)
	}
	if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS items_user_status_idx`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE items DROP COLUMN IF EXISTS status`); err != nil {
		t.Fatalf("drop column: %v", err)
	}

	// Step 2: insert "pre-007" items (no status column reference). 3 rows is
	// enough to confirm backfill runs across multiple rows.
	preIDs := []string{}
	for i, hash := range []string{"pre007-a", "pre007-b", "pre007-c"} {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO items (user_id, url, canonical_url, canonical_hash, title, fetch_status)
			VALUES ($1, $2, $2, $3, $4, 'success')
			RETURNING id
		`, userID, "https://example.invalid/"+hash, hash, "title-"+hash).Scan(&id); err != nil {
			t.Fatalf("seed pre-007 item #%d: %v", i, err)
		}
		preIDs = append(preIDs, id)
	}

	// Step 3: apply migrations/007 verbatim (the three statements in the
	// file). They are written to be idempotent (IF NOT EXISTS / DO $$
	// EXCEPTION) so we can run them inside the tx without depending on
	// the production-side pre-conditions.
	if _, err := tx.Exec(ctx, `
		ALTER TABLE items
		  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unread'
	`); err != nil {
		t.Fatalf("add column: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		DO $$
		BEGIN
		  ALTER TABLE items
		    ADD CONSTRAINT items_status_check
		    CHECK (status IN ('unread', 'read', 'archived'));
		EXCEPTION
		  WHEN duplicate_object THEN NULL;
		END
		$$
	`); err != nil {
		t.Fatalf("add constraint: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS items_user_status_idx
		  ON items (user_id, status, created_at DESC)
	`); err != nil {
		t.Fatalf("create index: %v", err)
	}

	// Step 4a: all pre-existing rows backfilled to 'unread' (Req 1.3 / 6.1).
	for _, id := range preIDs {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM items WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("read status for %q: %v", id, err)
		}
		if status != ItemStatusUnread {
			t.Errorf("backfilled status for %q = %q, want %q", id, status, ItemStatusUnread)
		}
	}

	// Step 4b: the CHECK constraint is active — an out-of-range value is
	// rejected. We try to flip the first row to a bogus value and confirm
	// the error. The check is done in a sub-transaction (SAVEPOINT) so the
	// failed statement does not abort the outer tx before rollback.
	if _, err := tx.Exec(ctx, `SAVEPOINT before_bogus`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	_, err = tx.Exec(ctx, `UPDATE items SET status = 'bogus' WHERE id = $1`, preIDs[0])
	if err == nil {
		t.Error("expected CHECK constraint violation when setting bogus status, got nil")
	}
	if _, rbErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT before_bogus`); rbErr != nil {
		t.Fatalf("rollback to savepoint: %v", rbErr)
	}
}

// TestCreateItem_DefaultsToUnread covers Req 1.2: a newly persisted item
// without an explicit status surfaces as 'unread'. This pins the
// "DEFAULT 'unread'" guarantee for the production CreateItem path (which
// does not write the status column in its INSERT).
func TestCreateItem_DefaultsToUnread(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	rawURL := "https://example.invalid/default-status"
	hash := "default-status-hash"
	itemID, created, err := s.CreateItem(ctx, userID, rawURL, rawURL, hash, nil, "default-title", "")
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true for fresh item, got false (id=%q)", itemID)
	}

	gotStatus, _ := readRowStatusAndFetchStatus(t, s, ctx, itemID)
	if gotStatus != ItemStatusUnread {
		t.Errorf("CreateItem default status = %q, want %q (DEFAULT 'unread')", gotStatus, ItemStatusUnread)
	}
}

// TestUpdateItemStatus_DoesNotMutateFetchStatus covers Req 1.6 (axis
// independence): user-status transitions never touch the fetch_status axis.
// Seeded items span all four fetch_status enum values; each is driven through
// the full unread -> read -> archived -> unread cycle and the fetch_status
// must remain at its seed value throughout.
func TestUpdateItemStatus_DoesNotMutateFetchStatus(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	fetchStatuses := []string{"success", "failed", "pending", "fetching"}
	transitions := []string{ItemStatusRead, ItemStatusArchived, ItemStatusUnread}

	for _, fs := range fetchStatuses {
		t.Run("fetch_status_"+fs, func(t *testing.T) {
			itemID := seedItemForStatusTest(t, s, ctx, userID, "axis-"+fs, ItemStatusUnread, fs)

			for _, next := range transitions {
				if _, err := s.UpdateItemStatus(ctx, userID, itemID, next); err != nil {
					t.Fatalf("UpdateItemStatus(%s -> %s): %v", "?", next, err)
				}
				gotStatus, gotFetch := readRowStatusAndFetchStatus(t, s, ctx, itemID)
				if gotStatus != next {
					t.Errorf("status after transition to %q = %q, want %q", next, gotStatus, next)
				}
				if gotFetch != fs {
					t.Errorf("fetch_status mutated by status transition: got %q, want %q (seed value)", gotFetch, fs)
				}
			}
		})
	}
}

// TestWorkerFetchUpdatesDoNotMutateStatus covers Req 1.6 (axis independence)
// from the other direction: the worker-side store functions
// (ClaimItemsForFetch / UpdateFetchSuccess / UpdateFetchFailure) must not
// touch items.status. Seeded items are pinned at 'read' / 'archived', and
// after each worker call we assert the status column has not changed.
func TestWorkerFetchUpdatesDoNotMutateStatus(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	// Seed two items with non-default statuses and fetch_status='pending' so
	// they are eligible for ClaimItemsForFetch.
	readID := seedItemForStatusTest(t, s, ctx, userID, "worker-read", ItemStatusRead, "pending")
	archivedID := seedItemForStatusTest(t, s, ctx, userID, "worker-archived", ItemStatusArchived, "pending")

	// ClaimItemsForFetch: flips fetch_status to 'fetching' but must leave
	// status untouched.
	claimed, err := s.ClaimItemsForFetch(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimItemsForFetch: %v", err)
	}
	claimedIDs := map[string]bool{}
	for _, it := range claimed {
		claimedIDs[it.ID] = true
	}
	if !claimedIDs[readID] || !claimedIDs[archivedID] {
		t.Fatalf("ClaimItemsForFetch did not claim both seeded items (got %d items: %v)", len(claimed), claimedIDs)
	}
	if gotStatus, _ := readRowStatusAndFetchStatus(t, s, ctx, readID); gotStatus != ItemStatusRead {
		t.Errorf("ClaimItemsForFetch mutated status for read item: got %q, want %q", gotStatus, ItemStatusRead)
	}
	if gotStatus, _ := readRowStatusAndFetchStatus(t, s, ctx, archivedID); gotStatus != ItemStatusArchived {
		t.Errorf("ClaimItemsForFetch mutated status for archived item: got %q, want %q", gotStatus, ItemStatusArchived)
	}

	// UpdateFetchSuccess: flips fetch_status to 'success' for one item. status
	// must remain unchanged.
	if err := s.UpdateFetchSuccess(ctx, readID, "new-title", "new-excerpt", "content", "search", 7); err != nil {
		t.Fatalf("UpdateFetchSuccess: %v", err)
	}
	if gotStatus, gotFetch := readRowStatusAndFetchStatus(t, s, ctx, readID); gotStatus != ItemStatusRead || gotFetch != "success" {
		t.Errorf("UpdateFetchSuccess: (status, fetch_status) = (%q, %q), want (%q, %q)",
			gotStatus, gotFetch, ItemStatusRead, "success")
	}

	// UpdateFetchFailure: flips fetch_status to 'failed' for the other item.
	// status must remain unchanged.
	if err := s.UpdateFetchFailure(ctx, archivedID, "boom"); err != nil {
		t.Fatalf("UpdateFetchFailure: %v", err)
	}
	if gotStatus, gotFetch := readRowStatusAndFetchStatus(t, s, ctx, archivedID); gotStatus != ItemStatusArchived || gotFetch != "failed" {
		t.Errorf("UpdateFetchFailure: (status, fetch_status) = (%q, %q), want (%q, %q)",
			gotStatus, gotFetch, ItemStatusArchived, "failed")
	}
}

// TestWebUpdateReflectsInMCPListRecent covers Req 5.4 (Web <-> MCP consistency):
// a Web-side state mutation (UpdateItemStatus) must be visible to the MCP
// `recent-articles` Resource's ListRecentItems call. Both paths share the
// single items table, so this regression-fixes the single-source-of-truth
// invariant.
func TestWebUpdateReflectsInMCPListRecent(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	itemID := seedItemForStatusTest(t, s, ctx, userID, "web-mcp", ItemStatusUnread, "success")

	// Act: simulate the Web-side PATCH /v1/items/{id}/status path.
	if _, err := s.UpdateItemStatus(ctx, userID, itemID, ItemStatusRead); err != nil {
		t.Fatalf("UpdateItemStatus: %v", err)
	}

	since := time.Now().Add(-1 * time.Hour)

	// Assert (case 1): the MCP `recent-articles` path (statuses=nil, the
	// fixed default for Resources) returns the item with the new status.
	rowsNil, err := s.ListRecentItems(ctx, userID, since, nil)
	if err != nil {
		t.Fatalf("ListRecentItems(nil): %v", err)
	}
	if found := findItemByID(rowsNil, itemID); found == nil {
		t.Fatalf("item not found in ListRecentItems(nil)")
	} else if found.Status != ItemStatusRead {
		t.Errorf("ListRecentItems(nil).Status = %q, want %q (Web update must be visible to MCP)", found.Status, ItemStatusRead)
	}

	// Assert (case 2): the explicit ["read"] filter (Tool path,
	// e.g. list_items?status=read) also returns the item.
	rowsRead, err := s.ListRecentItems(ctx, userID, since, []string{ItemStatusRead})
	if err != nil {
		t.Fatalf("ListRecentItems(read): %v", err)
	}
	if found := findItemByID(rowsRead, itemID); found == nil {
		t.Errorf("item not found in ListRecentItems([read]) — expected the updated row to match the explicit filter")
	} else if found.Status != ItemStatusRead {
		t.Errorf("ListRecentItems([read]).Status = %q, want %q", found.Status, ItemStatusRead)
	}
}

// findItemByID returns a pointer to the matching row from a ListRecentItems
// result, or nil if the id is absent. Keeping it as a local helper avoids
// adding production-side surface for what is purely a test affordance.
func findItemByID(rows []ItemListRow, id string) *ItemListRow {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

// equalStringSlices returns true if two []string have identical contents in
// order. Callers sort their inputs first when order does not matter.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
