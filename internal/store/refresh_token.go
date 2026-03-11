package store

import (
	"context"
	"time"
)

type ExtensionRefreshToken struct {
	ID         string
	UserID     string
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// CreateExtensionRefreshToken inserts a new refresh token record.
func (s *Store) CreateExtensionRefreshToken(ctx context.Context, userID, tokenHash string, ttl time.Duration) (ExtensionRefreshToken, error) {
	ttlSeconds := int64(ttl.Seconds())
	row := s.DB.QueryRow(ctx, `
		INSERT INTO extension_refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, NOW() + ($3::bigint * INTERVAL '1 second'))
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at
	`, userID, tokenHash, ttlSeconds)
	var rt ExtensionRefreshToken
	if err := row.Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &rt.CreatedAt, &rt.LastUsedAt); err != nil {
		return ExtensionRefreshToken{}, err
	}
	return rt, nil
}

// GetExtensionRefreshToken retrieves a valid (non-expired) refresh token by its hash.
func (s *Store) GetExtensionRefreshToken(ctx context.Context, tokenHash string) (ExtensionRefreshToken, error) {
	row := s.DB.QueryRow(ctx, `
		SELECT id, user_id, token_hash, expires_at, created_at, last_used_at
		FROM extension_refresh_tokens
		WHERE token_hash = $1 AND expires_at > NOW()
	`, tokenHash)
	var rt ExtensionRefreshToken
	if err := row.Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &rt.CreatedAt, &rt.LastUsedAt); err != nil {
		return ExtensionRefreshToken{}, err
	}
	return rt, nil
}

// TouchExtensionRefreshToken extends the expiration (sliding window) and updates last_used_at.
func (s *Store) TouchExtensionRefreshToken(ctx context.Context, id string, ttl time.Duration) error {
	ttlSeconds := int64(ttl.Seconds())
	_, err := s.DB.Exec(ctx, `
		UPDATE extension_refresh_tokens
		SET last_used_at = NOW(),
		    expires_at = NOW() + ($2::bigint * INTERVAL '1 second')
		WHERE id = $1
	`, id, ttlSeconds)
	return err
}

// DeleteExtensionRefreshToken removes a single refresh token (for logout).
func (s *Store) DeleteExtensionRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM extension_refresh_tokens WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteExtensionRefreshTokensByUser removes all refresh tokens for a user.
func (s *Store) DeleteExtensionRefreshTokensByUser(ctx context.Context, userID string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM extension_refresh_tokens WHERE user_id = $1`, userID)
	return err
}

// CleanupExpiredExtensionRefreshTokens removes all expired refresh tokens.
func (s *Store) CleanupExpiredExtensionRefreshTokens(ctx context.Context) (int64, error) {
	ct, err := s.DB.Exec(ctx, `DELETE FROM extension_refresh_tokens WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
