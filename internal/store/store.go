package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"altpocket/internal/crypto"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRefreshTokenDecryptFailed is returned by GetGoogleSheetsConnection
// when the persisted refresh_token cannot be base64-decoded, is shorter
// than the expected nonce, fails GCM authentication, or is a legacy
// plaintext value left over from before encryption was introduced.
//
// Callers must treat this error the same as pgx.ErrNoRows from the
// user's perspective ("not connected" / "needs re-authorization") so
// that decryption failures are surfaced as a recoverable UX state, not
// an internal server error. The underlying cause is intentionally
// indistinguishable to avoid leaking which decryption invariant failed.
var ErrRefreshTokenDecryptFailed = errors.New("store: refresh_token decrypt failed")

type Store struct {
	DB *pgxpool.Pool
	// encryptionKey is the AES-256 key used to encrypt and decrypt
	// at-rest refresh tokens. It is unexported (and never exposed via a
	// getter) so handlers cannot accidentally log or persist it.
	encryptionKey []byte
}

// New constructs a Store. encryptionKey must be exactly crypto.KeySize
// (32) bytes; it is used to encrypt/decrypt persisted Google Sheets
// refresh tokens. The caller is responsible for fail-fast validating
// the key at startup (see internal/config.Load).
func New(db *pgxpool.Pool, encryptionKey []byte) *Store {
	return &Store{DB: db, encryptionKey: encryptionKey}
}

type User struct {
	ID        string `json:"id"`
	GoogleSub string `json:"google_sub"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type GoogleSheetsConnection struct {
	UserID        string
	RefreshToken  string
	SpreadsheetID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ExportItemRow struct {
	ID          string
	URL         string
	Title       string
	Excerpt     string
	FetchStatus string
	FetchError  string
	CreatedAt   time.Time
	FetchedAt   *time.Time
	Tags        []string
}

type Tag struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	NormalizedName string `json:"normalized_name"`
	Count          int    `json:"count,omitempty"`
}

type Item struct {
	ID               string    `json:"id"`
	UserID           string    `json:"user_id"`
	URL              string    `json:"url"`
	CanonicalURL     string    `json:"canonical_url"`
	CanonicalHash    string    `json:"canonical_hash"`
	Title            string    `json:"title"`
	Excerpt          string    `json:"excerpt"`
	FetchStatus      string    `json:"fetch_status"`
	FetchError       string    `json:"fetch_error"`
	CreatedAt        time.Time `json:"created_at"`
	RefetchRequested bool      `json:"refetch_requested"`
}

type ItemDetail struct {
	Item
	ContentFull string `json:"content_full"`
	Tags        []Tag  `json:"tags"`
}

type ItemListRow struct {
	Item
	Tags []Tag `json:"tags"`
}

type Pagination struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Total   int `json:"total"`
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

// GetGoogleSheetsConnection reads the user's Google Sheets connection
// row, decodes the persisted refresh_token from base64, and decrypts
// it with the Store's AES-256 key.
//
// Returns:
//   - pgx.ErrNoRows when no connection record exists for userID.
//   - ErrRefreshTokenDecryptFailed when the persisted value is not a
//     valid base64 string, is too short to contain a nonce, fails GCM
//     authentication, or is a legacy plaintext refresh_token. Callers
//     must treat this case as "not connected" (Req 2.3 / Req 2.5).
//   - other errors for genuine DB-level failures.
//
// On success, GoogleSheetsConnection.RefreshToken contains the
// plaintext refresh token. Callers MUST NOT log, persist, or cache
// that value beyond the request lifetime (Req 2.2 / NFR 1.3).
func (s *Store) GetGoogleSheetsConnection(ctx context.Context, userID string) (GoogleSheetsConnection, error) {
	row := s.DB.QueryRow(ctx, `
		SELECT user_id, refresh_token, spreadsheet_id, created_at, updated_at
		FROM user_google_sheets_connections
		WHERE user_id=$1
	`, userID)
	var c GoogleSheetsConnection
	var stored string
	if err := row.Scan(&c.UserID, &stored, &c.SpreadsheetID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return GoogleSheetsConnection{}, err
	}

	blob, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		// Legacy plaintext rows (or any non-base64 string) collapse
		// here; they are surfaced as decrypt-failed so the user is
		// nudged to re-authorize. The original stored value is NOT
		// included in the error to avoid leaking ciphertext or
		// plaintext into logs (Req 1.5 / 2.4 / NFR 1.2).
		return GoogleSheetsConnection{}, ErrRefreshTokenDecryptFailed
	}
	plaintext, err := crypto.Decrypt(s.encryptionKey, blob)
	if err != nil {
		// Any decryption failure - wrong key, tampered ciphertext,
		// truncated blob - is collapsed into the sentinel error
		// (Req 2.3, 2.5).
		return GoogleSheetsConnection{}, ErrRefreshTokenDecryptFailed
	}
	c.RefreshToken = string(plaintext)
	return c, nil
}

// UpsertGoogleSheetsConnection encrypts refreshToken with the Store's
// AES-256 key, base64-encodes the resulting [nonce || ciphertext+tag]
// blob, and writes it to the user's connection row. Each call uses a
// fresh nonce (Req 1.3) so two upserts of the same plaintext produce
// different stored values, and the plaintext is never persisted (Req
// 1.1, 1.2).
//
// Returns crypto.ErrEmptyPlaintext if refreshToken is empty (callers
// should validate non-empty before invoking) and any DB error from the
// underlying UPSERT.
func (s *Store) UpsertGoogleSheetsConnection(ctx context.Context, userID, refreshToken string) error {
	blob, err := crypto.Encrypt(s.encryptionKey, []byte(refreshToken))
	if err != nil {
		// Wrap the crypto sentinel without including the plaintext.
		return fmt.Errorf("encrypt refresh_token: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(blob)
	_, err = s.DB.Exec(ctx, `
		INSERT INTO user_google_sheets_connections (user_id, refresh_token, spreadsheet_id)
		VALUES ($1, $2, '')
		ON CONFLICT (user_id) DO UPDATE
		SET refresh_token = EXCLUDED.refresh_token,
			updated_at = NOW()
	`, userID, encoded)
	return err
}

func (s *Store) SetGoogleSheetsSpreadsheetID(ctx context.Context, userID, spreadsheetID string) error {
	ct, err := s.DB.Exec(ctx, `
		UPDATE user_google_sheets_connections
		SET spreadsheet_id = $2, updated_at = NOW()
		WHERE user_id = $1
	`, userID, spreadsheetID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteGoogleSheetsConnection(ctx context.Context, userID string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM user_google_sheets_connections WHERE user_id=$1`, userID)
	return err
}

func (s *Store) ListItemsForExport(ctx context.Context, userID string) ([]ExportItemRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT i.id, i.url, i.title, i.excerpt, i.fetch_status, COALESCE(i.fetch_error, ''), i.created_at, i.fetched_at,
			COALESCE(array_agg(DISTINCT t.normalized_name ORDER BY t.normalized_name) FILTER (WHERE t.normalized_name IS NOT NULL), '{}') AS tags
		FROM items i
		LEFT JOIN item_tags it ON it.item_id = i.id
		LEFT JOIN tags t ON t.id = it.tag_id
		WHERE i.user_id = $1
		GROUP BY i.id
		ORDER BY i.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ExportItemRow{}
	for rows.Next() {
		var row ExportItemRow
		if err := rows.Scan(
			&row.ID,
			&row.URL,
			&row.Title,
			&row.Excerpt,
			&row.FetchStatus,
			&row.FetchError,
			&row.CreatedAt,
			&row.FetchedAt,
			&row.Tags,
		); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// CreateItem inserts a new item. tagNames should already be normalized for both display and key.
// title and excerpt are optional prefill values from the extension; empty strings are valid.
func (s *Store) CreateItem(ctx context.Context, userID, url, canonicalURL, canonicalHash string, tagNames []string, title, excerpt string) (string, bool, error) {
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
		INSERT INTO items (user_id, url, canonical_url, canonical_hash, title, excerpt, fetch_status, refetch_requested)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', false)
		ON CONFLICT (user_id, canonical_hash) DO NOTHING
		RETURNING id
	`, userID, url, canonicalURL, canonicalHash, title, excerpt)
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

func (s *Store) ListItems(ctx context.Context, userID string, page, perPage int, q string, tags []string, sort string) ([]ItemListRow, Pagination, error) {
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
	for _, selectedTag := range tags {
		where = append(where, fmt.Sprintf(`
			EXISTS (
				SELECT 1
				FROM item_tags itf
				JOIN tags tf ON tf.id = itf.tag_id
				WHERE itf.item_id = i.id AND tf.normalized_name = $%d
			)
		`, argPos))
		args = append(args, selectedTag)
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

// PatchItem updates an item's title and/or tags atomically within a transaction.
// Pass nil for title or tags to skip updating that field.
// Returns the current title and tags after the update.
func (s *Store) PatchItem(ctx context.Context, userID, itemID string, title *string, tags *[]string) (string, []Tag, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// Ownership check
	var currentTitle string
	if err = tx.QueryRow(ctx, `SELECT id, title FROM items WHERE id=$1 AND user_id=$2`, itemID, userID).Scan(&itemID, &currentTitle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, pgx.ErrNoRows
		}
		return "", nil, err
	}

	// Update title if specified
	updatedTitle := currentTitle
	if title != nil {
		_, err = tx.Exec(ctx, `UPDATE items SET title=$1 WHERE id=$2 AND user_id=$3`, *title, itemID, userID)
		if err != nil {
			return "", nil, err
		}
		updatedTitle = *title
	}

	// Replace tags if specified
	if tags != nil {
		_, err = tx.Exec(ctx, `DELETE FROM item_tags WHERE item_id=$1`, itemID)
		if err != nil {
			return "", nil, err
		}

		for _, name := range *tags {
			var tagID string
			if err = tx.QueryRow(ctx, `
				INSERT INTO tags (name, normalized_name)
				VALUES ($1, $2)
				ON CONFLICT (normalized_name) DO UPDATE SET name=EXCLUDED.name
				RETURNING id
			`, name, name).Scan(&tagID); err != nil {
				return "", nil, err
			}
			if _, err = tx.Exec(ctx, `
				INSERT INTO item_tags (item_id, tag_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, itemID, tagID); err != nil {
				return "", nil, err
			}
		}

		_, err = tx.Exec(ctx, `
			DELETE FROM tags t
			WHERE NOT EXISTS (SELECT 1 FROM item_tags it WHERE it.tag_id=t.id)
		`)
		if err != nil {
			return "", nil, err
		}
	}

	// Fetch current tags
	rows, err := tx.Query(ctx, `
		SELECT t.id, t.name, t.normalized_name
		FROM tags t
		JOIN item_tags it ON it.tag_id=t.id
		WHERE it.item_id=$1
		ORDER BY t.normalized_name
	`, itemID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()

	updatedTags := []Tag{}
	for rows.Next() {
		var t Tag
		if err = rows.Scan(&t.ID, &t.Name, &t.NormalizedName); err != nil {
			return "", nil, err
		}
		updatedTags = append(updatedTags, t)
	}
	if err = rows.Err(); err != nil {
		return "", nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return "", nil, err
	}
	return updatedTitle, updatedTags, nil
}

// ReplaceItemTags replaces all tags for an item. This is a wrapper around PatchItem
// that only updates tags, maintaining backward compatibility.
func (s *Store) ReplaceItemTags(ctx context.Context, userID, itemID string, tagNames []string) ([]Tag, error) {
	_, tags, err := s.PatchItem(ctx, userID, itemID, nil, &tagNames)
	return tags, err
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
	return s.ListTagsWithCountFiltered(ctx, userID, "", nil)
}

// TagsByNormalizedNames returns Tag rows whose `normalized_name` is in the
// supplied slice and which appear on at least one item owned by userID. Names
// that do not exist, or that exist only on other users' items, are silently
// absent from the result.
//
// Used by the /ui/items fragment renderer (Issue #115) to resolve active filter
// chip display names when the full Tags facet query is skipped. AC 1.3
// requires chips to show the original user-entered name (e.g. "Go Lang") even
// when filter results are zero, so the handler queries the tag rows directly
// rather than depending on items[*].Tags or the facet aggregate.
//
// The lookup is intentionally cheap — items / item_tags are already joined by
// the rest of the items handler — and is scoped by user_id so that another
// user's tag display name cannot leak into the current viewer's chip even when
// the same normalized_name exists across users (round-4 review of PR #137).
func (s *Store) TagsByNormalizedNames(ctx context.Context, userID string, normalizedNames []string) ([]Tag, error) {
	if len(normalizedNames) == 0 {
		return nil, nil
	}
	rows, err := s.DB.Query(ctx, `
		SELECT DISTINCT t.id, t.name, t.normalized_name
		FROM tags t
		JOIN item_tags it ON it.tag_id = t.id
		JOIN items i ON i.id = it.item_id
		WHERE i.user_id = $1 AND t.normalized_name = ANY($2)
	`, userID, normalizedNames)
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

func (s *Store) ListTagsWithCountFiltered(ctx context.Context, userID, q string, selectedTags []string) ([]Tag, error) {
	where := []string{"i.user_id = $1"}
	args := []interface{}{userID}
	argPos := 2

	if q != "" {
		where = append(where, fmt.Sprintf("(i.title ILIKE $%d OR i.excerpt ILIKE $%d OR c.content_search ILIKE $%d OR i.canonical_url ILIKE $%d OR t.normalized_name ILIKE $%d)", argPos, argPos, argPos, argPos, argPos))
		args = append(args, "%"+q+"%")
		argPos++
	}
	for _, selectedTag := range selectedTags {
		where = append(where, fmt.Sprintf(`
			EXISTS (
				SELECT 1
				FROM item_tags itf
				JOIN tags tf ON tf.id = itf.tag_id
				WHERE itf.item_id = i.id AND tf.normalized_name = $%d
			)
		`, argPos))
		args = append(args, selectedTag)
		argPos++
	}

	whereSQL := strings.Join(where, " AND ")
	query := fmt.Sprintf(`
		WITH filtered_items AS (
			SELECT DISTINCT i.id
			FROM items i
			LEFT JOIN item_contents c ON c.item_id=i.id
			LEFT JOIN item_tags it ON it.item_id=i.id
			LEFT JOIN tags t ON t.id=it.tag_id
			WHERE %s
		)
		SELECT t.id, t.name, t.normalized_name, COUNT(DISTINCT it.item_id) AS count
		FROM filtered_items fi
		JOIN item_tags it ON it.item_id = fi.id
		JOIN tags t ON t.id = it.tag_id
		GROUP BY t.id
		ORDER BY t.normalized_name
	`, whereSQL)

	rows, err := s.DB.Query(ctx, query, args...)
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

	tag, err := tx.Exec(ctx, `
		UPDATE items
		SET title=$1, excerpt=$2, fetch_status='success', fetch_error='', fetched_at=NOW(), refetch_requested=false
		WHERE id=$3
		  AND (fetch_status <> 'success' OR refetch_requested=true)
	`, title, excerpt, itemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
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
		  AND (fetch_status <> 'success' OR refetch_requested=true)
	`, reason, itemID)
	return err
}

func (s *Store) UpdateCapturedContent(ctx context.Context, userID, itemID, title, excerpt, contentFull, contentSearch string, contentBytes int) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	tag, err := tx.Exec(ctx, `
		UPDATE items
		SET title = CASE WHEN $1 <> '' THEN $1 ELSE title END,
			excerpt = $2,
			fetch_status = 'success',
			fetch_error = '',
			fetched_at = NOW(),
			refetch_requested = false
		WHERE id = $3 AND user_id = $4
	`, title, excerpt, itemID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO item_contents (item_id, content_full, content_search, content_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (item_id) DO UPDATE
		SET content_full = EXCLUDED.content_full,
			content_search = EXCLUDED.content_search,
			content_bytes = EXCLUDED.content_bytes
	`, itemID, contentFull, contentSearch, contentBytes)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) SeedCapturedContent(ctx context.Context, userID, itemID, title, excerpt, contentFull, contentSearch string, contentBytes int) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	tag, err := tx.Exec(ctx, `
		UPDATE items
		SET title = CASE WHEN $1 <> '' THEN $1 ELSE title END,
			excerpt = CASE WHEN excerpt = '' THEN $2 ELSE excerpt END
		WHERE id = $3 AND user_id = $4 AND fetch_status <> 'success'
	`, title, excerpt, itemID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO item_contents (item_id, content_full, content_search, content_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (item_id) DO UPDATE
		SET content_full = EXCLUDED.content_full,
			content_search = EXCLUDED.content_search,
			content_bytes = EXCLUDED.content_bytes
	`, itemID, contentFull, contentSearch, contentBytes)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
