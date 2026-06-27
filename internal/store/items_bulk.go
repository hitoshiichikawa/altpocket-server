package store

import (
	"context"
)

// BulkTagResult is the per-item result of BulkAddItemTag. It carries the
// item_id whose tag set was updated and the FULL post-update tag list
// (existing tags + newly added tag), ordered by normalized_name. The
// caller (server.handleBulkTagItems) returns Tags to the client so the UI
// can rerender the card chip row without an additional fetch (Req 5.5).
//
// Only items that were confirmed to be owned by userID (resolved via
// SELECT ... FOR KEY SHARE inside BulkAddItemTag) appear in the
// succeeded slice — items owned by another user or that no longer exist
// are simply absent, and the handler reports them to the client as
// failed[{reason: "not_found"}] (Req 8.1 / 8.2 / 8.3).
type BulkTagResult struct {
	ItemID string `json:"item_id"`
	Tags   []Tag  `json:"tags"`
}

// BulkDeleteItems atomically deletes the items in itemIDs that are owned
// by userID, returning the slice of item IDs that were actually deleted.
// Items owned by another user or that do not exist are silently absent
// from the return value (the handler reports them to the client as
// failed[{reason: "not_found"}] / Req 4.4 / 4.5 / 8.1 / 8.2 / 8.3).
//
// All work runs inside a single transaction so that orphan item_contents
// / item_tags rows are cleaned up in the same atomic step as the parent
// items rows. The orphan tags sweep at the tail mirrors the existing
// DeleteItem behavior (store.go: DeleteItem) so the global tags table
// does not grow indefinitely when the last item carrying a tag is
// deleted.
//
// The SQL casts every []string parameter to uuid[] explicitly
// (ANY($1::uuid[])) because items.id / item_contents.item_id /
// item_tags.item_id are UUID columns. pgx v5 encodes []string as text[]
// by default; without the cast, PostgreSQL rejects the comparison with
// "operator does not exist: uuid = text" at execution time (design.md
// File Structure Plan / task 1 pgx v5 + UUID 列の型整合 note).
//
// Empty input short-circuits to ([]string{}, nil) so callers may forward
// a 0-length list without a database round-trip.
func (s *Store) BulkDeleteItems(ctx context.Context, userID string, itemIDs []string) (succeeded []string, err error) {
	if len(itemIDs) == 0 {
		return []string{}, nil
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// item_contents and item_tags must be deleted before items because
	// of the FK references. The EXISTS sub-select with user_id pins the
	// cleanup to rows whose parent items belongs to userID, so cross-user
	// IDs are silently ignored (Req 8.1 / 8.2).
	if _, err = tx.Exec(ctx, `
		DELETE FROM item_contents
		WHERE item_id = ANY($1::uuid[])
		  AND EXISTS (SELECT 1 FROM items WHERE id = item_contents.item_id AND user_id = $2)
	`, itemIDs, userID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM item_tags
		WHERE item_id = ANY($1::uuid[])
		  AND EXISTS (SELECT 1 FROM items WHERE id = item_tags.item_id AND user_id = $2)
	`, itemIDs, userID); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		DELETE FROM items
		WHERE id = ANY($1::uuid[]) AND user_id = $2
		RETURNING id
	`, itemIDs, userID)
	if err != nil {
		return nil, err
	}
	succeeded = []string{}
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			err = scanErr
			return nil, err
		}
		succeeded = append(succeeded, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}

	// Orphan tag sweep — mirrors DeleteItem (store.go) so the shared
	// tags table does not grow indefinitely when the last item carrying
	// a tag is deleted. Bounded to tags with zero remaining item_tags
	// references, so other users' tags are never affected.
	if _, err = tx.Exec(ctx, `
		DELETE FROM tags t
		WHERE NOT EXISTS (SELECT 1 FROM item_tags it WHERE it.tag_id = t.id)
	`); err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return succeeded, nil
}

// BulkAddItemTag atomically adds tagInput to every item in itemIDs that
// is owned by userID, returning per-item BulkTagResult entries containing
// the FULL post-update tag list (existing + newly added). Items owned by
// another user or that no longer exist are silently absent from the
// return value (the handler reports them to the client as
// failed[{reason: "not_found"}] / Req 5.3 / 5.4 / 8.1 / 8.2 / 8.3).
//
// The transaction structure is deliberately ordered to (a) prevent a
// race where step 1's ownership check observes a row that step 4's
// INSERT then fails on with a FK violation, and (b) avoid creating an
// orphan tags row when every requested ID turns out to be unauthorized
// or already deleted:
//
//  1. SELECT ... FOR KEY SHARE on items resolves the OWNED set and
//     pins those rows against concurrent DELETE-from-items until commit
//     (KEY SHARE blocks the FOR KEY UPDATE that DELETE acquires, while
//     two concurrent BulkAddItemTag calls do NOT block each other).
//     Without this lock, step 4 could see "row vanished mid-tx" and
//     collapse the whole bulk into a 500 db_error.
//  2. If no items are owned, return early with an empty result. The
//     tags row is NOT inserted — otherwise an authorization-failed
//     request would silently pollute the global tags table and tag
//     suggestions / chip filters.
//  3. Upsert the tags row (shared global table keyed by
//     normalized_name) and capture its id.
//  4. INSERT ... ON CONFLICT DO NOTHING populates item_tags for every
//     OWNED item, deduplicating against rows that already carry the
//     same (item_id, tag_id) pair (Req 5.4 — no duplicate addition).
//     display_name carries the user-entered casing per item per user
//     (migration 006).
//  5. SELECT the FULL post-update tag set for the OWNED items so the
//     return value carries the chip row contents the UI needs.
//
// pgx v5 + UUID column type integrity: every ANY($N) and unnest($N)
// parameter is cast to uuid[] explicitly because items.id /
// item_tags.item_id are UUID columns and pgx v5 encodes []string as
// text[] (design.md / task 1 cast note).
//
// Empty input short-circuits to ([]BulkTagResult{}, nil).
func (s *Store) BulkAddItemTag(ctx context.Context, userID string, itemIDs []string, tagInput TagInput) (succeeded []BulkTagResult, err error) {
	if len(itemIDs) == 0 {
		return []BulkTagResult{}, nil
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// Step 1: own check + row-level KEY SHARE lock.
	// FOR KEY SHARE blocks concurrent DELETE-from-items (which takes
	// FOR KEY UPDATE) until this tx commits, but stays compatible with
	// other FOR KEY SHARE acquisitions so two parallel BulkAddItemTag
	// calls on overlapping ID sets do not deadlock.
	ownedItemIDs := make([]string, 0, len(itemIDs))
	{
		rows, qerr := tx.Query(ctx, `
			SELECT id FROM items
			WHERE id = ANY($1::uuid[]) AND user_id = $2
			FOR KEY SHARE
		`, itemIDs, userID)
		if qerr != nil {
			err = qerr
			return nil, err
		}
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				rows.Close()
				err = scanErr
				return nil, err
			}
			ownedItemIDs = append(ownedItemIDs, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}

	// Step 2: early return guard.
	// Returning here AVOIDS the tags upsert in step 3 so that a
	// fully-unauthorized request (all IDs belong to other users / are
	// already deleted) does not leak a global tags row as a side
	// effect (Req 8.2 / 8.3 — failed targets unchanged).
	if len(ownedItemIDs) == 0 {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return []BulkTagResult{}, nil
	}

	// Step 3: tags upsert (shared global table keyed by
	// normalized_name). The no-op DO UPDATE makes RETURNING fire for
	// the existing row when the tag already exists, mirroring the
	// pattern used by upsertItemTags in store.go.
	var tagID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO tags (name, normalized_name)
		VALUES ($1, $2)
		ON CONFLICT (normalized_name) DO UPDATE SET normalized_name = tags.normalized_name
		RETURNING id
	`, tagInput.Name, tagInput.NormalizedName).Scan(&tagID); err != nil {
		return nil, err
	}

	// Step 4: item_tags add (ON CONFLICT DO NOTHING dedupes against
	// existing (item_id, tag_id) rows so the tag is added at most once
	// per item — Req 5.4). display_name is the user-entered casing per
	// user per item (migration 006). The unnest($1::uuid[]) feeds the
	// pre-checked OWNED set, so the FK to items.id can never fail
	// (step 1's KEY SHARE lock kept those rows alive until commit).
	if _, err = tx.Exec(ctx, `
		INSERT INTO item_tags (item_id, tag_id, display_name)
		SELECT id, $1, $2 FROM unnest($3::uuid[]) AS id
		ON CONFLICT (item_id, tag_id) DO NOTHING
	`, tagID, tagInput.Name, ownedItemIDs); err != nil {
		return nil, err
	}

	// Step 5: FULL post-update tag set per item.
	// ORDER BY it.item_id keeps consecutive rows together so a single
	// linear scan can group them into BulkTagResult; the secondary
	// ORDER BY t.normalized_name keeps the chip order deterministic
	// for both UI rendering and tests.
	rows, err := tx.Query(ctx, `
		SELECT it.item_id, t.id, it.display_name, t.normalized_name
		FROM item_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id = ANY($1::uuid[])
		ORDER BY it.item_id, t.normalized_name
	`, ownedItemIDs)
	if err != nil {
		return nil, err
	}
	tagsByItem := make(map[string][]Tag, len(ownedItemIDs))
	for rows.Next() {
		var itemID, tID, displayName, normalizedName string
		if scanErr := rows.Scan(&itemID, &tID, &displayName, &normalizedName); scanErr != nil {
			rows.Close()
			err = scanErr
			return nil, err
		}
		tagsByItem[itemID] = append(tagsByItem[itemID], Tag{
			ID:             tID,
			Name:           displayName,
			NormalizedName: normalizedName,
		})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}

	succeeded = make([]BulkTagResult, 0, len(ownedItemIDs))
	for _, id := range ownedItemIDs {
		tags := tagsByItem[id]
		if tags == nil {
			// Defensive: an OWNED item should have at least the
			// newly-added tag row, but if step 4's ON CONFLICT
			// suppressed every row (already-present tag on an
			// item with no other tags is impossible — the newly
			// inserted row IS the tag — but we still tolerate
			// nil maps to avoid panicking on edge schema state).
			tags = []Tag{}
		}
		succeeded = append(succeeded, BulkTagResult{
			ItemID: id,
			Tags:   tags,
		})
	}
	return succeeded, nil
}

