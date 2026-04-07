package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeKeyValidator struct {
	validHash string
	userID    string
	err       error

	gotHash string
}

func (f *fakeKeyValidator) ValidateMCPAPIKey(_ context.Context, hash string) (string, error) {
	f.gotHash = hash
	if f.err != nil {
		return "", f.err
	}
	if hash != f.validHash {
		return "", errors.New("not found")
	}
	return f.userID, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func runMiddleware(t *testing.T, v KeyValidator, header string) (*httptest.ResponseRecorder, string, bool) {
	t.Helper()
	var (
		seenUserID string
		nextCalled bool
	)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		seenUserID = UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mw := NewAuthMiddleware(v, discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	return rec, seenUserID, nextCalled
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	rec, _, called := runMiddleware(t, &fakeKeyValidator{}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("next handler must not be called when header is missing")
	}
}

func TestAuthMiddleware_NonBearerScheme(t *testing.T) {
	rec, _, called := runMiddleware(t, &fakeKeyValidator{}, "Basic dXNlcjpwYXNz")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("next handler must not be called for non-Bearer scheme")
	}
}

func TestAuthMiddleware_EmptyBearerToken(t *testing.T) {
	rec, _, called := runMiddleware(t, &fakeKeyValidator{}, "Bearer ")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("next handler must not be called for empty token")
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	v := &fakeKeyValidator{validHash: "different"}
	rec, _, called := runMiddleware(t, v, "Bearer some-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("next handler must not be called for invalid token")
	}
}

func TestAuthMiddleware_ValidTokenSetsUserID(t *testing.T) {
	token := "valid-token-xyz"
	sum := sha256.Sum256([]byte(token))
	expectedHash := hex.EncodeToString(sum[:])
	v := &fakeKeyValidator{validHash: expectedHash, userID: "user-42"}

	rec, seenUserID, called := runMiddleware(t, v, "Bearer "+token)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("next handler should be called for a valid token")
	}
	if seenUserID != "user-42" {
		t.Fatalf("expected userID 'user-42' in context, got %q", seenUserID)
	}
	if v.gotHash != expectedHash {
		t.Fatalf("expected validator to receive SHA-256 hash %q, got %q", expectedHash, v.gotHash)
	}
}

func TestUserIDFromContext_AbsentReturnsEmpty(t *testing.T) {
	if got := UserIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
