package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altpocket/internal/auth"
)

func TestHandleListItemsUnauthorizedReturnsJSONError(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/items?page=1&per_page=50&sort=newest", nil)
	rr := httptest.NewRecorder()

	s.handleListItems(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %q", rr.Body.String())
	}
}

func TestHandleCreateItemUnauthorizedReturnsJSONError(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader(`{"url":"https://example.com"}`))
	rr := httptest.NewRecorder()

	s.handleCreateItem(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %q", rr.Body.String())
	}
}

func TestHandleCreateItemInvalidURLReturnsErrorCode(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader(`{"url":"http://[::1","tags":["go"]}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), auth.User{ID: "user-1"}))
	rr := httptest.NewRecorder()

	s.handleCreateItem(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_url") {
		t.Fatalf("expected invalid_url body, got %q", rr.Body.String())
	}
}

func TestHandleCreateItemWithTitleExcerptAndInvalidURLReturnsInvalidURL(t *testing.T) {
	s := newAuthTestServer()
	body := `{"url":"http://[::1","tags":["go"],"title":"Test Title","excerpt":"Test Excerpt"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithUser(req.Context(), auth.User{ID: "user-1"}))
	rr := httptest.NewRecorder()

	s.handleCreateItem(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_url") {
		t.Fatalf("expected invalid_url (not invalid_request), got %q", rr.Body.String())
	}
}

func TestHandleCreateItemWithoutTitleExcerptBackwardCompatible(t *testing.T) {
	s := newAuthTestServer()
	body := `{"url":"http://[::1","tags":["go"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithUser(req.Context(), auth.User{ID: "user-1"}))
	rr := httptest.NewRecorder()

	s.handleCreateItem(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_url") {
		t.Fatalf("expected invalid_url (backward compatible), got %q", rr.Body.String())
	}
}

func TestHandleCaptureItemContentUnauthorizedReturnsJSONError(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/item-1/capture", strings.NewReader(`{"title":"t","content_full":"body"}`))
	rr := httptest.NewRecorder()

	s.handleCaptureItemContent(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %q", rr.Body.String())
	}
}

func TestHandleCaptureItemContentRejectsBlankContent(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/items/item-1/capture", strings.NewReader(`{"title":"t","content_full":"   "}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), auth.User{ID: "user-1"}))
	rr := httptest.NewRecorder()

	s.handleCaptureItemContent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Fatalf("expected invalid_request body, got %q", rr.Body.String())
	}
}
