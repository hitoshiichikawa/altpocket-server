package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env               string
	HTTPAddr          string
	DatabaseURL       string
	SessionSecret     string
	JWTSecret         string
	GoogleWebClientID string
	GoogleExtClientID string
	GoogleClientSecret string
	PublicBaseURL     string
	ContentFullLimit  int
	ContentSearchLimit int
}

func Load() Config {
	return Config{
		Env:                getEnv("APP_ENV", "development"),
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
	}
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
