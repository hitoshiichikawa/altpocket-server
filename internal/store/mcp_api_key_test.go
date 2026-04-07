//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the real SQL against a Postgres database. They are
// gated by `-tags=integration` and the TEST_DATABASE_URL env var so they
// don't run in default unit-test invocations.
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/store/...
//
// The test database must have schema migrations 001..004 applied.

func newIntegrationStore(t *testing.T) (*Store, func()) {
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

// seedTestUser creates a throwaway user row and returns its ID. The mcp_api_keys
// table has a FK to users(id) so we need a real user before exercising the keys.
func seedTestUser(t *testing.T, s *Store) string {
	t.Helper()
	var id string
	err := s.DB.QueryRow(context.Background(), `
		INSERT INTO users (google_sub, email, name)
		VALUES ($1, $2, $3)
		RETURNING id
	`, "test-sub-"+t.Name(), t.Name()+"@example.invalid", t.Name()).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestMCPAPIKey_CreateListValidateDelete(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	token := "integration-test-token"
	hash := hashOf(token)
	prefix := token[:8]

	// Create
	created, err := s.CreateMCPAPIKey(ctx, userID, hash, prefix)
	if err != nil {
		t.Fatalf("CreateMCPAPIKey: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if created.UserID != userID {
		t.Fatalf("expected UserID %q, got %q", userID, created.UserID)
	}
	if created.KeyPrefix != prefix {
		t.Fatalf("expected prefix %q, got %q", prefix, created.KeyPrefix)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("expected non-zero CreatedAt")
	}

	// List
	keys, err := s.ListMCPAPIKeys(ctx, userID)
	if err != nil {
		t.Fatalf("ListMCPAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != created.ID {
		t.Fatalf("expected 1 key with id %q, got %+v", created.ID, keys)
	}
	if keys[0].KeyHash != "" {
		t.Fatal("ListMCPAPIKeys must not expose the hash")
	}

	// Validate (success)
	gotUserID, err := s.ValidateMCPAPIKey(ctx, hash)
	if err != nil {
		t.Fatalf("ValidateMCPAPIKey: %v", err)
	}
	if gotUserID != userID {
		t.Fatalf("expected userID %q, got %q", userID, gotUserID)
	}

	// Validate (unknown hash)
	if _, err := s.ValidateMCPAPIKey(ctx, hashOf("not-a-real-token")); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for unknown hash, got %v", err)
	}

	// Delete (cross-user must fail)
	otherUserID := seedTestUser(t, s)
	if err := s.DeleteMCPAPIKey(ctx, otherUserID, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows when deleting another user's key, got %v", err)
	}

	// Delete (owner)
	if err := s.DeleteMCPAPIKey(ctx, userID, created.ID); err != nil {
		t.Fatalf("DeleteMCPAPIKey: %v", err)
	}

	// Validate after delete
	if _, err := s.ValidateMCPAPIKey(ctx, hash); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows after delete, got %v", err)
	}

	// Delete again must report not-found
	if err := s.DeleteMCPAPIKey(ctx, userID, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows on double delete, got %v", err)
	}
}

func TestMCPAPIKey_DuplicateHashRejected(t *testing.T) {
	s, cleanup := newIntegrationStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := seedTestUser(t, s)

	hash := hashOf("dup-token")
	if _, err := s.CreateMCPAPIKey(ctx, userID, hash, "dup00000"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM mcp_api_keys WHERE user_id = $1`, userID)
	})

	if _, err := s.CreateMCPAPIKey(ctx, userID, hash, "dup00000"); err == nil {
		t.Fatal("expected unique-violation on duplicate hash, got nil")
	}
}
