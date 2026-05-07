package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"altpocket/internal/crypto"
)

type Config struct {
	Env                string
	HTTPAddr           string
	DatabaseURL        string
	SessionSecret      string
	JWTSecret          string
	GoogleWebClientID  string
	GoogleExtClientID  string
	GoogleClientSecret string
	PublicBaseURL      string
	ContentFullLimit   int
	ContentSearchLimit int
	CORSAllowOrigins   []string
	// EncryptionKey is the 32-byte AES-256 key used to encrypt at-rest
	// sensitive values (currently the Google Sheets refresh_token).
	// It is decoded from the base64-encoded ENCRYPTION_KEY env var on
	// startup. The raw key value is never logged.
	EncryptionKey []byte
}

func Load() Config {
	env := getEnv("APP_ENV", "development")
	corsAllowOrigins := getEnvList("CORS_ALLOW_ORIGINS")
	if env == "production" && len(corsAllowOrigins) == 0 {
		panic("missing env: CORS_ALLOW_ORIGINS")
	}

	encryptionKey := mustDecodeEncryptionKey("ENCRYPTION_KEY")

	return Config{
		Env:                env,
		HTTPAddr:           getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:        mustEnv("DATABASE_URL"),
		SessionSecret:      mustEnv("SESSION_SECRET"),
		JWTSecret:          mustEnv("JWT_SECRET"),
		GoogleWebClientID:  mustEnv("GOOGLE_WEB_CLIENT_ID"),
		GoogleExtClientID:  mustEnv("GOOGLE_EXT_CLIENT_ID"),
		GoogleClientSecret: mustEnv("GOOGLE_CLIENT_SECRET"),
		PublicBaseURL:      mustEnv("PUBLIC_BASE_URL"),
		ContentFullLimit:   getEnvInt("CONTENT_FULL_LIMIT_BYTES", 1_000_000),
		ContentSearchLimit: getEnvInt("CONTENT_SEARCH_LIMIT_BYTES", 16_384),
		CORSAllowOrigins:   corsAllowOrigins,
		EncryptionKey:      encryptionKey,
	}
}

// mustDecodeEncryptionKey reads, base64-decodes, and length-validates
// the AES-256 key from the named env var. It panics with a descriptive
// (but key-free) message on any failure to enforce fail-fast startup
// behavior. The raw key value never appears in panic messages.
func mustDecodeEncryptionKey(envName string) []byte {
	raw := os.Getenv(envName)
	if raw == "" {
		panic("missing env: " + envName)
	}
	key, err := crypto.DecodeKey(raw)
	if err != nil {
		switch {
		case errors.Is(err, crypto.ErrMalformedKey):
			panic("invalid env: " + envName + " (base64 decode failed)")
		case errors.Is(err, crypto.ErrInvalidKeyLength):
			panic("invalid env: " + envName + " (must be 32 bytes after base64 decode)")
		default:
			// Defensive: unknown crypto error. Avoid leaking the key in
			// the message by using a generic phrase.
			panic("invalid env: " + envName + " (decode failed)")
		}
	}
	return key
}

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic("missing env: " + key)
	}
	return v
}

func getEnvList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func SessionTTL() time.Duration {
	return 7 * 24 * time.Hour
}

// ExtensionJWTTTL is the lifetime of short-lived JWTs issued for the Chrome extension.
func ExtensionJWTTTL() time.Duration {
	return 1 * time.Hour
}

// ExtensionRefreshTokenTTL is the sliding-window lifetime of extension refresh tokens.
// The expiration is extended by this duration on each use.
func ExtensionRefreshTokenTTL() time.Duration {
	return 30 * 24 * time.Hour
}
