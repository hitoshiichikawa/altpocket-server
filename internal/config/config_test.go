package config

import "testing"

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db?sslmode=disable")
	t.Setenv("SESSION_SECRET", "session-secret")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("GOOGLE_WEB_CLIENT_ID", "web-client-id")
	t.Setenv("GOOGLE_EXT_CLIENT_ID", "ext-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("PUBLIC_BASE_URL", "https://api.example.test")
}

func TestLoadPanicsWithoutCORSAllowOriginsInProduction(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("CORS_ALLOW_ORIGINS", "")

	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic when CORS_ALLOW_ORIGINS is missing in production")
		}
	}()

	_ = Load()
}

func TestLoadAllowsEmptyCORSAllowOriginsInDevelopment(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("CORS_ALLOW_ORIGINS", "")

	cfg := Load()
	if len(cfg.CORSAllowOrigins) != 0 {
		t.Fatalf("expected empty CORS allow list, got %#v", cfg.CORSAllowOrigins)
	}
}
