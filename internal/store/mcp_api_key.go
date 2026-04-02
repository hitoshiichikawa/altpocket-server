package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// MCPAPIKey represents an API key for MCP Bearer Token authentication.
type MCPAPIKey struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	KeyHash   string    `json:"-"`
	KeyPrefix string    `json:"key_prefix"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateMCPAPIKey stores a hashed API key with its display prefix.
func (s *Store) CreateMCPAPIKey(ctx context.Context, userID, keyHash, keyPrefix string) (MCPAPIKey, error) {
	row := s.DB.QueryRow(ctx, `
		INSERT INTO mcp_api_keys (user_id, key_hash, key_prefix)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, key_hash, key_prefix, created_at
	`, userID, keyHash, keyPrefix)
	var k MCPAPIKey
	if err := row.Scan(&k.ID, &k.UserID, &k.KeyHash, &k.KeyPrefix, &k.CreatedAt); err != nil {
		return MCPAPIKey{}, err
	}
	return k, nil
}

// ListMCPAPIKeys returns all API keys for a user (prefix and metadata only).
func (s *Store) ListMCPAPIKeys(ctx context.Context, userID string) ([]MCPAPIKey, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, user_id, key_prefix, created_at
		FROM mcp_api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []MCPAPIKey
	for rows.Next() {
		var k MCPAPIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.KeyPrefix, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// ValidateMCPAPIKey looks up an API key by its SHA-256 hash and returns the owning user ID.
// Returns pgx.ErrNoRows if no matching key is found.
func (s *Store) ValidateMCPAPIKey(ctx context.Context, keyHash string) (string, error) {
	var userID string
	err := s.DB.QueryRow(ctx, `
		SELECT user_id FROM mcp_api_keys WHERE key_hash = $1
	`, keyHash).Scan(&userID)
	if err != nil {
		return "", err
	}
	return userID, nil
}

// DeleteMCPAPIKey removes an API key, scoped to the owning user.
// Returns pgx.ErrNoRows if the key doesn't exist or belongs to another user.
func (s *Store) DeleteMCPAPIKey(ctx context.Context, userID, keyID string) error {
	ct, err := s.DB.Exec(ctx, `
		DELETE FROM mcp_api_keys WHERE id = $1 AND user_id = $2
	`, keyID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
