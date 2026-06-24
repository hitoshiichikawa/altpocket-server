package store

import (
	"context"
	"fmt"
	"time"
)

// ListRecentItems returns items created after since, with tags, ordered
// by newest first. The optional statuses argument (nil / empty = no
// filter) AND-joins `i.status = ANY($N)` to the WHERE clause when
// non-empty so MCP Tool callers can subset by the 3-value user-visible
// state introduced by Issue #119. The MCP `recent-articles` Resource
// (recentArticlesHandler) always passes nil because Resources do not
// accept structured input arguments — Req 5.2 is satisfied by fixing
// that default to "all states" rather than by exposing a per-call
// filter (design.md "`recent-articles` Resource の status 引数取扱").
func (s *Store) ListRecentItems(ctx context.Context, userID string, since time.Time, statuses []string) ([]ItemListRow, error) {
	where := "i.user_id = $1 AND i.created_at >= $2"
	args := []interface{}{userID, since}
	if len(statuses) > 0 {
		where += fmt.Sprintf(" AND i.status = ANY($%d)", len(args)+1)
		args = append(args, statuses)
	}

	query := fmt.Sprintf(`
		SELECT i.id, i.user_id, i.url, i.canonical_url, i.canonical_hash, i.title, i.excerpt,
			i.fetch_status, COALESCE(i.fetch_error,''), i.status, i.created_at, i.refetch_requested,
			COALESCE(array_agg(DISTINCT t.id) FILTER (WHERE t.id IS NOT NULL), '{}') AS tag_ids,
			COALESCE(array_agg(DISTINCT t.name) FILTER (WHERE t.name IS NOT NULL), '{}') AS tag_names,
			COALESCE(array_agg(DISTINCT t.normalized_name) FILTER (WHERE t.normalized_name IS NOT NULL), '{}') AS tag_norms
		FROM items i
		LEFT JOIN item_tags it ON it.item_id = i.id
		LEFT JOIN tags t ON t.id = it.tag_id
		WHERE %s
		GROUP BY i.id
		ORDER BY i.created_at DESC
	`, where)

	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ItemListRow{}
	for rows.Next() {
		var row ItemListRow
		var tagIDs, tagNames, tagNorms []string
		if err := rows.Scan(&row.ID, &row.UserID, &row.URL, &row.CanonicalURL, &row.CanonicalHash,
			&row.Title, &row.Excerpt, &row.FetchStatus, &row.FetchError, &row.Status, &row.CreatedAt,
			&row.RefetchRequested, &tagIDs, &tagNames, &tagNorms); err != nil {
			return nil, err
		}
		row.Tags = make([]Tag, 0, len(tagIDs))
		for i := range tagIDs {
			row.Tags = append(row.Tags, Tag{ID: tagIDs[i], Name: tagNames[i], NormalizedName: tagNorms[i]})
		}
		items = append(items, row)
	}
	return items, rows.Err()
}
