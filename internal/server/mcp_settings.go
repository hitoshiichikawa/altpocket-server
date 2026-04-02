package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"altpocket/internal/auth"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleMCPKeyGenerate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/ui/settings?status=unauthorized", http.StatusFound)
		return
	}

	csrfExpected := s.csrfFromContext(r.Context())
	csrfProvided := r.PostFormValue("csrf_token")
	if csrfExpected == "" || csrfProvided == "" || csrfExpected != csrfProvided {
		http.Redirect(w, r, "/ui/settings?status=csrf_error", http.StatusFound)
		return
	}

	// Generate 32 random bytes → 64 char hex string
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Redirect(w, r, "/ui/settings?status=mcp_key_failed", http.StatusFound)
		return
	}
	plainKey := hex.EncodeToString(raw)
	prefix := plainKey[:8]

	hash := sha256.Sum256([]byte(plainKey))
	keyHash := hex.EncodeToString(hash[:])

	if _, err := s.store.CreateMCPAPIKey(r.Context(), user.ID, keyHash, prefix); err != nil {
		http.Redirect(w, r, "/ui/settings?status=mcp_key_failed", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/ui/settings?status=mcp_key_created&mcp_key="+plainKey, http.StatusFound)
}

func (s *Server) handleMCPKeyRevoke(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/ui/settings?status=unauthorized", http.StatusFound)
		return
	}

	csrfExpected := s.csrfFromContext(r.Context())
	csrfProvided := r.PostFormValue("csrf_token")
	if csrfExpected == "" || csrfProvided == "" || csrfExpected != csrfProvided {
		http.Redirect(w, r, "/ui/settings?status=csrf_error", http.StatusFound)
		return
	}

	keyID := chi.URLParam(r, "id")
	if keyID == "" {
		http.Redirect(w, r, "/ui/settings?status=mcp_key_failed", http.StatusFound)
		return
	}

	if err := s.store.DeleteMCPAPIKey(r.Context(), user.ID, keyID); err != nil {
		http.Redirect(w, r, "/ui/settings?status=mcp_key_failed", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/ui/settings?status=mcp_key_revoked", http.StatusFound)
}

func settingsMCPNotice(status string) (string, string) {
	switch status {
	case "mcp_key_created":
		return "", ""
	case "mcp_key_revoked":
		return "API key has been revoked.", "notice notice-success"
	case "mcp_key_failed":
		return "Failed to manage API key.", "notice notice-error"
	default:
		return "", ""
	}
}

func (s *Server) mcpEndpointURL() string {
	return strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/mcp"
}
