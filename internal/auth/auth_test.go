package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIssueAndParseJWTRoundTrip(t *testing.T) {
	secret := "test-secret"
	userID := "user-123"

	token, _, err := IssueJWT(secret, userID, 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueJWT failed: %v", err)
	}

	gotUserID, err := ParseJWT(secret, token)
	if err != nil {
		t.Fatalf("ParseJWT failed: %v", err)
	}
	if gotUserID != userID {
		t.Fatalf("expected user_id %q, got %q", userID, gotUserID)
	}
}

func TestParseJWTRejectsWrongSecret(t *testing.T) {
	token, _, err := IssueJWT("secret-a", "user-123", 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueJWT failed: %v", err)
	}

	if _, err := ParseJWT("secret-b", token); err == nil {
		t.Fatalf("expected ParseJWT to reject token with wrong secret")
	}
}

func TestParseJWTRejectsTamperedToken(t *testing.T) {
	token, _, err := IssueJWT("test-secret", "user-123", 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueJWT failed: %v", err)
	}

	tampered := token[:len(token)-1] + "x"
	if _, err := ParseJWT("test-secret", tampered); err == nil {
		t.Fatalf("expected ParseJWT to reject tampered token")
	}
}

func TestParseJWTRejectsExpiredToken(t *testing.T) {
	token, _, err := IssueJWT("test-secret", "user-123", -1*time.Minute)
	if err != nil {
		t.Fatalf("IssueJWT failed: %v", err)
	}

	if _, err := ParseJWT("test-secret", token); err == nil {
		t.Fatalf("expected ParseJWT to reject expired token")
	}
}

func TestParseJWTRejectsMissingSub(t *testing.T) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	})
	tokenString, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign token failed: %v", err)
	}

	if _, err := ParseJWT("test-secret", tokenString); err == nil || !strings.Contains(err.Error(), "missing sub") {
		t.Fatalf("expected missing sub error, got: %v", err)
	}
}

func TestParseJWTRejectsNonHS256Algorithm(t *testing.T) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"sub": "user-123",
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	})
	tokenString, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign token failed: %v", err)
	}

	if _, err := ParseJWT("test-secret", tokenString); err == nil {
		t.Fatalf("expected ParseJWT to reject non-HS256 token")
	}
}

func TestParseJWTRejectsNoneAlgorithm(t *testing.T) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "user-123",
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	})
	tokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign token failed: %v", err)
	}

	if _, err := ParseJWT("test-secret", tokenString); err == nil {
		t.Fatalf("expected ParseJWT to reject none-algorithm token")
	}
}
