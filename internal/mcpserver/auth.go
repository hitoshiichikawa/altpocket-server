package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"altpocket/internal/store"
)

type contextKey string

const userIDKey contextKey = "mcp_user_id"

// UserIDFromContext returns the authenticated MCP user ID from the request context.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

// NewAuthMiddleware returns a chi middleware that validates Bearer tokens
// against the mcp_api_keys table. On success it stores the user ID in context.
func NewAuthMiddleware(st *store.Store, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				log.Warn("mcp_auth_failed", "reason", "missing_bearer_token", "remote", r.RemoteAddr)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(auth, "Bearer ")
			hash := sha256.Sum256([]byte(token))
			keyHash := hex.EncodeToString(hash[:])

			userID, err := st.ValidateMCPAPIKey(r.Context(), keyHash)
			if err != nil {
				log.Warn("mcp_auth_failed", "reason", "invalid_token", "remote", r.RemoteAddr)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
