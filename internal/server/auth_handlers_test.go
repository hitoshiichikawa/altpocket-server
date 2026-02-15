package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altpocket/internal/config"
	"altpocket/internal/ratelimit"

	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

func newAuthTestServer() *Server {
	cfg := config.Config{
		GoogleWebClientID:  "web-client-id",
		GoogleExtClientID:  "ext-client-id",
		GoogleClientSecret: "web-client-secret",
		PublicBaseURL:      "https://www.example.invalid",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, nil, ratelimit.New(60, 60), logger, nil)
}

func TestHandleGoogleLoginStateGenerationFailure(t *testing.T) {
	s := newAuthTestServer()
	s.randomStringFn = func(int) (string, error) {
		return "", errors.New("entropy failure")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/google/login", nil)
	rr := httptest.NewRecorder()

	s.handleGoogleLogin(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "failed") {
		t.Fatalf("expected body to contain failure message, got %q", rr.Body.String())
	}
}

func TestHandleGoogleCallbackInvalidState(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/google/callback?state=expected", nil)
	rr := httptest.NewRecorder()

	s.handleGoogleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid state") {
		t.Fatalf("expected invalid state body, got %q", rr.Body.String())
	}
}

func TestHandleGoogleCallbackMissingCode(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/google/callback?state=state-ok", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state-ok"})
	rr := httptest.NewRecorder()

	s.handleGoogleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "missing code") {
		t.Fatalf("expected missing code body, got %q", rr.Body.String())
	}
}

func TestHandleGoogleCallbackExchangeFailed(t *testing.T) {
	s := newAuthTestServer()
	s.oauthExchangeFn = func(context.Context, string) (*oauth2.Token, error) {
		return nil, errors.New("oauth exchange failed")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/google/callback?state=state-ok&code=test-code", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state-ok"})
	rr := httptest.NewRecorder()

	s.handleGoogleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "exchange failed") {
		t.Fatalf("expected exchange failed body, got %q", rr.Body.String())
	}
}

func TestHandleGoogleCallbackMissingIDToken(t *testing.T) {
	s := newAuthTestServer()
	s.oauthExchangeFn = func(context.Context, string) (*oauth2.Token, error) {
		return &oauth2.Token{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/google/callback?state=state-ok&code=test-code", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state-ok"})
	rr := httptest.NewRecorder()

	s.handleGoogleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "missing id_token") {
		t.Fatalf("expected missing id_token body, got %q", rr.Body.String())
	}
}

func TestHandleGoogleCallbackInvalidIDToken(t *testing.T) {
	s := newAuthTestServer()
	s.oauthExchangeFn = func(context.Context, string) (*oauth2.Token, error) {
		return (&oauth2.Token{}).WithExtra(map[string]any{
			"id_token": "invalid-token",
		}), nil
	}

	var gotAudience string
	var gotToken string
	s.idTokenValidateFn = func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
		gotToken = token
		gotAudience = audience
		return nil, errors.New("invalid token")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/google/callback?state=state-ok&code=test-code", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state-ok"})
	rr := httptest.NewRecorder()

	s.handleGoogleCallback(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid id_token") {
		t.Fatalf("expected invalid id_token body, got %q", rr.Body.String())
	}
	if gotAudience != s.cfg.GoogleWebClientID {
		t.Fatalf("expected audience %q, got %q", s.cfg.GoogleWebClientID, gotAudience)
	}
	if gotToken != "invalid-token" {
		t.Fatalf("expected token to be passed to validator")
	}
}

func TestHandleGoogleCallbackMissingSubject(t *testing.T) {
	s := newAuthTestServer()
	s.oauthExchangeFn = func(context.Context, string) (*oauth2.Token, error) {
		return (&oauth2.Token{}).WithExtra(map[string]any{
			"id_token": "valid-but-missing-subject",
		}), nil
	}
	s.idTokenValidateFn = func(context.Context, string, string) (*idtoken.Payload, error) {
		return &idtoken.Payload{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/google/callback?state=state-ok&code=test-code", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state-ok"})
	rr := httptest.NewRecorder()

	s.handleGoogleCallback(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid id_token") {
		t.Fatalf("expected invalid id_token body, got %q", rr.Body.String())
	}
}

func TestHandleExtensionExchangeInvalidRequest(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/extension/exchange", strings.NewReader("{"))
	rr := httptest.NewRecorder()

	s.handleExtensionExchange(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}

func TestHandleExtensionExchangeMissingIDToken(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/extension/exchange", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()

	s.handleExtensionExchange(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}

func TestHandleExtensionExchangeInvalidToken(t *testing.T) {
	s := newAuthTestServer()
	var gotAudience string
	s.idTokenValidateFn = func(_ context.Context, _, audience string) (*idtoken.Payload, error) {
		gotAudience = audience
		return nil, errors.New("invalid token")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/extension/exchange", strings.NewReader(`{"id_token":"bad"}`))
	rr := httptest.NewRecorder()

	s.handleExtensionExchange(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_token") {
		t.Fatalf("expected invalid_token body, got %q", rr.Body.String())
	}
	if gotAudience != s.cfg.GoogleExtClientID {
		t.Fatalf("expected audience %q, got %q", s.cfg.GoogleExtClientID, gotAudience)
	}
}

func TestHandleExtensionExchangeMissingSubject(t *testing.T) {
	s := newAuthTestServer()
	s.idTokenValidateFn = func(context.Context, string, string) (*idtoken.Payload, error) {
		return &idtoken.Payload{}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/extension/exchange", strings.NewReader(`{"id_token":"valid-but-missing-subject"}`))
	rr := httptest.NewRecorder()

	s.handleExtensionExchange(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_token") {
		t.Fatalf("expected invalid_token body, got %q", rr.Body.String())
	}
}

func TestCheckCSRFFailsWithoutSessionCookieOnMutatingRequest(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items", nil)

	if err := s.checkCSRF(req); err == nil {
		t.Fatalf("expected csrf check to fail without session cookie")
	}
}

func TestCheckCSRFSkipsBearerRequests(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items", nil)
	req.Header.Set("Authorization", "Bearer test")

	if err := s.checkCSRF(req); err != nil {
		t.Fatalf("expected csrf check to be skipped for bearer auth, got: %v", err)
	}
}
