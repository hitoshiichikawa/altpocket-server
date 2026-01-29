package store

import (
	"context"
	"time"
)

type Session struct {
	ID        string
	UserID    string
	CSRFToken string
	ExpiresAt time.Time
}

func (s *Store) CreateSession(ctx context.Context, userID, csrfToken string, ttl time.Duration) (Session, error) {
	row := s.DB.QueryRow(ctx, `
		INSERT INTO sessions (user_id, csrf_token, expires_at)
		VALUES ($1, $2, NOW() + ($3 || ' seconds')::interval)
		RETURNING id, user_id, csrf_token, expires_at
	`, userID, csrfToken, int(ttl.Seconds()))
	var sess Session
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &sess.ExpiresAt); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	row := s.DB.QueryRow(ctx, `
		SELECT id, user_id, csrf_token, expires_at
		FROM sessions
		WHERE id=$1 AND expires_at > NOW()
	`, id)
	var sess Session
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &sess.ExpiresAt); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	return err
}

func (s *Store) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	ct, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
