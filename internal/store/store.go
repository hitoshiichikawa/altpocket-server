package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	DB *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{DB: db}
}

type User struct {
	ID        string
	GoogleSub string
	Email     string
	Name      string
	AvatarURL string
}

type Tag struct {
	ID             string
	Name           string
	NormalizedName string
	Count          int
}

type Item struct {
	ID               string
	UserID           string
	URL              string
	CanonicalURL     string
	CanonicalHash    string
	Title            string
	Excerpt          string
	FetchStatus      string
	FetchError       string
	CreatedAt        time.Time
	RefetchRequested bool
}

type ItemDetail struct {
	Item
	ContentFull string
	Tags        []Tag
}

type ItemListRow struct {
	Item
	Tags []Tag
}

type Pagination struct {
	Page    int
	PerPage int
	Total   int
}

func (s *Store) UpsertUser(ctx context.Context, sub, email, name, avatar string) (User, error) {
	row := s.DB.QueryRow(ctx, `
		INSERT INTO users (google_sub, email, name, avatar_url)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (google_sub)
		DO UPDATE SET email=EXCLUDED.email, name=EXCLUDED.name, avatar_url=EXCLUDED.avatar_url
		RETURNING id, google_sub, email, name, avatar_url
	`, sub, email, name, avatar)
	var u User
	if err := row.Scan(&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.AvatarURL); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) GetUserBySub(ctx context.Context, sub string) (User, error) {
	row := s.DB.QueryRow(ctx, `SELECT id, google_sub, email, name, avatar_url FROM users WHERE google_sub=$1`, sub)
	var u User
	if err := row.Scan(&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.AvatarURL); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id string) (User, error) {
	row := s.DB.QueryRow(ctx, `SELECT id, google_sub, email, name, avatar_url FROM users WHERE id=$1`, id)
	var u User
	if err := row.Scan(&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.AvatarURL); err != nil {
		return User{}, err
	}
	return u, nil
}

// CreateItem inserts a new item. tagNames should already be normalized for both display and key.
func (s *Store) CreateItem(ctx context.Context, userID, url, canonicalURL, canonicalHash string, tagNames []string) (string, bool, error) {
	var itemID string
	created := false

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	row := tx.QueryRow(ctx, `
		INSERT INTO items (user_id, url, canonical_url, canonical_hash, fetch_status, refetch_requested)
		VALUES ($1, $2, $3, $4, 'pending', false)
		ON CONFLICT (user_id, canonical_hash) DO NOTHING
		RETURNING id
	`, userID, url, canonicalURL, canonicalHash)
	if err = row.Scan(&itemID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			row = tx.QueryRow(ctx, `SELECT id FROM items WHERE user_id=$1 AND canonical_hash=$2`, userID, canonicalHash)
			if err = row.Scan(&itemID); err != nil {
				return "", false, err
			}
			created = false
		} else {
			return "", false, err
		}
	} else {
		created = true
	}

	if created && len(tagNames) > 0 {
		for _, name := range tagNames {
			var tagID string
			row = tx.QueryRow(ctx, `
				INSERT INTO tags (name, normalized_name)
				VALUES ($1, $2)
				ON CONFLICT (normalized_name) DO UPDATE SET name=EXCLUDED.name
				RETURNING id
			`, name, name)
			if err = row.Scan(&tagID); err != nil {
				return "", false, err
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO item_tags (item_id, tag_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, itemID, tagID)
			if err != nil {
				return "", false, err
			}
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return "", false, err
	}

	return itemID, created, nil
}

func (s *Store) ListItems(ctx context.Context, userID string, page, perPage int, q, tag, sort string) ([]ItemListRow, Pagination, error) {
	if page < 1 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 30
	}
	offset := (page - 1) * perPage

	where := []string{"i.user_id = $1"}
	args := []interface{}{userID}
	argPos := 2

	if q != "" {
		where = append(where, fmt.Sprintf("(i.title ILIKE $%d OR i.excerpt ILIKE $%d OR c.content_search ILIKE $%d OR i.canonical_url ILIKE $%d OR t.normalized_name ILIKE $%d)", argPos, argPos, argPos, argPos, argPos))
		args = append(args, "%"+q+"%")
		argPos++
	}
	if tag != "" {
		where = append(where, fmt.Sprintf("t.normalized_name = $%d", argPos))
		args = append(args, tag)
		argPos++
	}

	whereSQL := strings.Join(where, " AND ")
	orderBy := "i.created_at DESC"
	if sort == "relevance" && q != "" {
		orderBy = "score DESC, i.created_at DESC"
	}

	countSQL := fmt.Sprintf(`
		SELECT COUNT(DISTINCT i.id)
		FROM items i
		LEFT JOIN item_contents c ON c.item_id=i.id
		LEFT JOIN item_tags it ON it.item_id=i.id
		LEFT JOIN tags t ON t.id=it.tag_id
		WHERE %s
	`, whereSQL)
	var total int
	if err := s.DB.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, Pagination{}, err
	}

	selectSQL := fmt.Sprintf(`
		SELECT i.id, i.user_id, i.url, i.canonical_url, i.canonical_hash, i.title, i.excerpt,
			i.fetch_status, COALESCE(i.fetch_error,''), i.created_at, i.refetch_requested,
			COALESCE(array_agg(DISTINCT t.id) FILTER (WHERE t.id IS NOT NULL), '{}') AS tag_ids,
			COALESCE(array_agg(DISTINCT t.name) FILTER (WHERE t.name IS NOT NULL), '{}') AS tag_names,
			COALESCE(array_agg(DISTINCT t.normalized_name) FILTER (WHERE t.normalized_name IS NOT NULL), '{}') AS tag_norms,
			COALESCE(
				similarity(i.title, $%d) +
				similarity(i.excerpt, $%d) +
				similarity(c.content_search, $%d) +
				similarity(i.canonical_url, $%d) +
				COALESCE(MAX(similarity(t.normalized_name, $%d)), 0),
				0
			) AS score
		FROM items i
		LEFT JOIN item_contents c ON c.item_id=i.id
		LEFT JOIN item_tags it ON it.item_id=i.id
		LEFT JOIN tags t ON t.id=it.tag_id
		WHERE %s
		GROUP BY i.id, c.content_search
		ORDER BY %s
		LIMIT %d OFFSET %d
	`, argPos, argPos, argPos, argPos, argPos, whereSQL, orderBy, perPage, offset)

	argsSelect := make([]interface{}, 0, len(args)+1)
	argsSelect = append(argsSelect, args...)
	if q == "" {
		argsSelect = append(argsSelect, "")
	} else {
		argsSelect = append(argsSelect, q)
	}

	rows, err := s.DB.Query(ctx, selectSQL, argsSelect...)
	if err != nil {
		return nil, Pagination{}, err
	}
	defer rows.Close()

	items := []ItemListRow{}
	for rows.Next() {
		var row ItemListRow
		var tagIDs []string
		var tagNames []string
		var tagNorms []string
		var score float64
		if err := rows.Scan(&row.ID, &row.UserID, &row.URL, &row.CanonicalURL, &row.CanonicalHash, &row.Title, &row.Excerpt,
			&row.FetchStatus, &row.FetchError, &row.CreatedAt, &row.RefetchRequested, &tagIDs, &tagNames, &tagNorms, &score); err != nil {
			return nil, Pagination{}, err
		}
		row.Tags = make([]Tag, 0, len(tagIDs))
		for i := range tagIDs {
			row.Tags = append(row.Tags, Tag{ID: tagIDs[i], Name: tagNames[i], NormalizedName: tagNorms[i]})
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, Pagination{}, err
	}

	return items, Pagination{Page: page, PerPage: perPage, Total: total}, nil
}

func (s *Store) GetItemDetail(ctx context.Context, userID, itemID string) (ItemDetail, error) {
	row := s.DB.QueryRow(ctx, `
		SELECT i.id, i.user_id, i.url, i.canonical_url, i.canonical_hash, i.title, i.excerpt,
			i.fetch_status, COALESCE(i.fetch_error,''), i.created_at, i.refetch_requested,
			COALESCE(c.content_full,''),
			COALESCE(array_agg(DISTINCT t.id) FILTER (WHERE t.id IS NOT NULL), '{}') AS tag_ids,
			COALESCE(array_agg(DISTINCT t.name) FILTER (WHERE t.name IS NOT NULL), '{}') AS tag_names,
			COALESCE(array_agg(DISTINCT t.normalized_name) FILTER (WHERE t.normalized_name IS NOT NULL), '{}') AS tag_norms
		FROM items i
		LEFT JOIN item_contents c ON c.item_id=i.id
		LEFT JOIN item_tags it ON it.item_id=i.id
		LEFT JOIN tags t ON t.id=it.tag_id
		WHERE i.user_id=$1 AND i.id=$2
		GROUP BY i.id, c.content_full
	`, userID, itemID)
	var detail ItemDetail
	var tagIDs []string
	var tagNames []string
	var tagNorms []string
	if err := row.Scan(&detail.ID, &detail.UserID, &detail.URL, &detail.CanonicalURL, &detail.CanonicalHash, &detail.Title, &detail.Excerpt,
		&detail.FetchStatus, &detail.FetchError, &detail.CreatedAt, &detail.RefetchRequested, &detail.ContentFull, &tagIDs, &tagNames, &tagNorms); err != nil {
		return ItemDetail{}, err
	}
	detail.Tags = make([]Tag, 0, len(tagIDs))
	for i := range tagIDs {
		detail.Tags = append(detail.Tags, Tag{ID: tagIDs[i], Name: tagNames[i], NormalizedName: tagNorms[i]})
	}
	return detail, nil
}

func (s *Store) DeleteItem(ctx context.Context, userID, itemID string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	_, err = tx.Exec(ctx, `DELETE FROM item_contents WHERE item_id=$1 AND EXISTS (SELECT 1 FROM items WHERE id=$1 AND user_id=$2)`, itemID, userID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM item_tags WHERE item_id=$1 AND EXISTS (SELECT 1 FROM items WHERE id=$1 AND user_id=$2)`, itemID, userID)
	if err != nil {
		return err
	}
	ct, err := tx.Exec(ctx, `DELETE FROM items WHERE id=$1 AND user_id=$2`, itemID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	_, err = tx.Exec(ctx, `
		DELETE FROM tags t
		WHERE NOT EXISTS (SELECT 1 FROM item_tags it WHERE it.tag_id=t.id)
	`)
	if err != nil {
		return err
	}

	err = tx.Commit(ctx)
	return err
}

func (s *Store) RequestRefetch(ctx context.Context, userID, itemID string) error {
	ct, err := s.DB.Exec(ctx, `
		UPDATE items SET refetch_requested=true WHERE id=$1 AND user_id=$2
	`, itemID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) SuggestTags(ctx context.Context, q string) ([]Tag, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, name, normalized_name FROM tags
		WHERE normalized_name ILIKE $1
		ORDER BY normalized_name
		LIMIT 20
	`, "%"+q+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.NormalizedName); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

func (s *Store) ListTagsWithCount(ctx context.Context, userID string) ([]Tag, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT t.id, t.name, t.normalized_name, COUNT(it.item_id) AS count
		FROM tags t
		JOIN item_tags it ON it.tag_id=t.id
		JOIN items i ON i.id=it.item_id
		WHERE i.user_id=$1
		GROUP BY t.id
		ORDER BY t.normalized_name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.NormalizedName, &t.Count); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// ClaimItemsForFetch selects up to limit items and marks them as fetching.
func (s *Store) ClaimItemsForFetch(ctx context.Context, limit int) ([]Item, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	rows, err := tx.Query(ctx, `
		SELECT id, user_id, url, refetch_requested
		FROM items
		WHERE fetch_status='pending' OR refetch_requested=true
		ORDER BY created_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.UserID, &it.URL, &it.RefetchRequested); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, it := range items {
		_, err = tx.Exec(ctx, `
			UPDATE items
			SET fetch_status='fetching', fetch_attempts=fetch_attempts+1, last_fetch_attempt_at=NOW()
			WHERE id=$1
		`, it.ID)
		if err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) UpdateFetchSuccess(ctx context.Context, itemID, title, excerpt, contentFull, contentSearch string, contentBytes int) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	_, err = tx.Exec(ctx, `
		UPDATE items
		SET title=$1, excerpt=$2, fetch_status='success', fetch_error='', fetched_at=NOW(), refetch_requested=false
		WHERE id=$3
	`, title, excerpt, itemID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO item_contents (item_id, content_full, content_search, content_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (item_id) DO UPDATE SET content_full=EXCLUDED.content_full, content_search=EXCLUDED.content_search, content_bytes=EXCLUDED.content_bytes
	`, itemID, contentFull, contentSearch, contentBytes)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) UpdateFetchFailure(ctx context.Context, itemID, reason string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE items
		SET fetch_status='failed', fetch_error=$1, refetch_requested=false
		WHERE id=$2
	`, reason, itemID)
	return err
}
