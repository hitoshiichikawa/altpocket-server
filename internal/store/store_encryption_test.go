//go:build integration

package store

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the real SQL plus the encryption pipeline
// against a Postgres database. They are gated by `-tags=integration`
// and the TEST_DATABASE_URL env var so they don't run in default unit
// invocations.
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/store/...
//
// The test database must have schema migrations 001..005 applied
// (005 is the forward-only invalidation of legacy plaintext rows; the
// tests below upsert their own data so the table just needs to exist).

func newEncryptionStore(t *testing.T) (*Store, []byte, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	key := newRandomKey(t)
	return New(pool, key), key, func() { pool.Close() }
}

func newRandomKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(cryptorand.Reader, key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return key
}

// seedSheetsUser creates a throwaway user for FK satisfaction and
// schedules its row (and any cascaded sheets connection) for cleanup.
func seedSheetsUser(t *testing.T, s *Store) string {
	t.Helper()
	var id string
	err := s.DB.QueryRow(context.Background(), `
		INSERT INTO users (google_sub, email, name)
		VALUES ($1, $2, $3)
		RETURNING id
	`, "sheets-sub-"+t.Name(), t.Name()+"@example.invalid", t.Name()).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(context.Background(), `DELETE FROM user_google_sheets_connections WHERE user_id = $1`, id)
		_, _ = s.DB.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// TestUpsertAndGetGoogleSheetsConnection_RoundTrip covers Req 1.1 / 2.1:
// a refresh_token written via Upsert must be returned in plaintext by
// Get when the same key is in use.
func TestUpsertAndGetGoogleSheetsConnection_RoundTrip(t *testing.T) {
	s, _, cleanup := newEncryptionStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedSheetsUser(t, s)

	const refreshToken = "1//refresh-token-roundtrip"

	if err := s.UpsertGoogleSheetsConnection(ctx, userID, refreshToken); err != nil {
		t.Fatalf("UpsertGoogleSheetsConnection: %v", err)
	}
	conn, err := s.GetGoogleSheetsConnection(ctx, userID)
	if err != nil {
		t.Fatalf("GetGoogleSheetsConnection: %v", err)
	}
	if conn.UserID != userID {
		t.Fatalf("conn.UserID = %q, want %q", conn.UserID, userID)
	}
	if conn.RefreshToken != refreshToken {
		t.Fatalf("conn.RefreshToken = %q, want %q", conn.RefreshToken, refreshToken)
	}
}

// TestUpsertGoogleSheetsConnection_PersistedValueIsNotPlaintext
// covers Req 1.1 / 1.2: after Upsert, the raw column value must NOT
// equal the original refresh_token string and must look like a base64
// blob (so a DB dump won't reveal the token).
func TestUpsertGoogleSheetsConnection_PersistedValueIsNotPlaintext(t *testing.T) {
	s, _, cleanup := newEncryptionStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedSheetsUser(t, s)

	const refreshToken = "1//refresh-token-not-plaintext"

	if err := s.UpsertGoogleSheetsConnection(ctx, userID, refreshToken); err != nil {
		t.Fatalf("UpsertGoogleSheetsConnection: %v", err)
	}

	var stored string
	if err := s.DB.QueryRow(ctx, `SELECT refresh_token FROM user_google_sheets_connections WHERE user_id=$1`, userID).Scan(&stored); err != nil {
		t.Fatalf("SELECT raw refresh_token: %v", err)
	}
	if stored == refreshToken {
		t.Fatal("persisted refresh_token equals plaintext (encryption did not run)")
	}
	blob, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		t.Fatalf("persisted refresh_token is not valid base64: %v", err)
	}
	// nonce(12) + ciphertext (>= len(plaintext)) + tag(16)
	if len(blob) < 12+len(refreshToken)+16 {
		t.Fatalf("persisted blob length %d looks too short for nonce+ciphertext+tag", len(blob))
	}
}

// TestGetGoogleSheetsConnection_LegacyPlaintextRejected covers Req 2.5:
// rows whose refresh_token column still holds a pre-encryption plaintext
// must surface as ErrRefreshTokenDecryptFailed (the migration-005 sweep
// is operator-driven; this test exists to defend the read path even if
// a row slips through).
func TestGetGoogleSheetsConnection_LegacyPlaintextRejected(t *testing.T) {
	s, _, cleanup := newEncryptionStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedSheetsUser(t, s)

	// Inject a row whose refresh_token is the legacy plaintext shape.
	// The Google refresh tokens that were stored before this migration
	// began with "1//" and contained slashes; they are NOT valid
	// base64 at the standard alphabet so Get must reject them at the
	// base64 step. We pick a value that is also not coincidentally
	// decodable (no padding, contains '/' which IS a base64 char, but
	// the length is not a multiple of 4 to force a base64 error).
	const legacy = "1//legacy-plaintext-refresh-token"
	if _, err := s.DB.Exec(ctx, `
		INSERT INTO user_google_sheets_connections (user_id, refresh_token, spreadsheet_id)
		VALUES ($1, $2, '')
	`, userID, legacy); err != nil {
		t.Fatalf("seed legacy plaintext row: %v", err)
	}

	_, err := s.GetGoogleSheetsConnection(ctx, userID)
	if !errors.Is(err, ErrRefreshTokenDecryptFailed) {
		t.Fatalf("expected ErrRefreshTokenDecryptFailed for legacy plaintext, got %v", err)
	}
}

// TestGetGoogleSheetsConnection_WrongKeyRejected covers Req 2.3: a Get
// performed with a different encryption key (e.g. after rotation) must
// surface as ErrRefreshTokenDecryptFailed so the user is nudged to
// re-authorize rather than receiving an internal error.
func TestGetGoogleSheetsConnection_WrongKeyRejected(t *testing.T) {
	s, _, cleanup := newEncryptionStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedSheetsUser(t, s)

	if err := s.UpsertGoogleSheetsConnection(ctx, userID, "rt-key-rotation"); err != nil {
		t.Fatalf("UpsertGoogleSheetsConnection: %v", err)
	}

	// Construct a second store with a different key but the same DB
	// pool. Sharing the pool keeps the test scoped to the encryption
	// boundary (the row is real, only the key changed).
	rotated := New(s.DB, newRandomKey(t))
	if _, err := rotated.GetGoogleSheetsConnection(ctx, userID); !errors.Is(err, ErrRefreshTokenDecryptFailed) {
		t.Fatalf("expected ErrRefreshTokenDecryptFailed under rotated key, got %v", err)
	}
}

// TestUpsertGoogleSheetsConnection_NonceUniqueness covers Req 1.3:
// upserting the same plaintext twice must produce two different
// stored values because the nonce is fresh per call.
func TestUpsertGoogleSheetsConnection_NonceUniqueness(t *testing.T) {
	s, _, cleanup := newEncryptionStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedSheetsUser(t, s)

	const refreshToken = "1//refresh-token-nonce"

	if err := s.UpsertGoogleSheetsConnection(ctx, userID, refreshToken); err != nil {
		t.Fatalf("first UpsertGoogleSheetsConnection: %v", err)
	}
	var first string
	if err := s.DB.QueryRow(ctx, `SELECT refresh_token FROM user_google_sheets_connections WHERE user_id=$1`, userID).Scan(&first); err != nil {
		t.Fatalf("SELECT first: %v", err)
	}

	if err := s.UpsertGoogleSheetsConnection(ctx, userID, refreshToken); err != nil {
		t.Fatalf("second UpsertGoogleSheetsConnection: %v", err)
	}
	var second string
	if err := s.DB.QueryRow(ctx, `SELECT refresh_token FROM user_google_sheets_connections WHERE user_id=$1`, userID).Scan(&second); err != nil {
		t.Fatalf("SELECT second: %v", err)
	}

	if first == second {
		t.Fatalf("two upserts produced identical persisted values (nonce reuse?)\nfirst:  %s\nsecond: %s", first, second)
	}

	// Sanity: the second blob must still round-trip to the same plaintext.
	conn, err := s.GetGoogleSheetsConnection(ctx, userID)
	if err != nil {
		t.Fatalf("GetGoogleSheetsConnection after second upsert: %v", err)
	}
	if conn.RefreshToken != refreshToken {
		t.Fatalf("post-upsert RefreshToken = %q, want %q", conn.RefreshToken, refreshToken)
	}
}
