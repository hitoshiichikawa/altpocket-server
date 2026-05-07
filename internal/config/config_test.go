package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// validEncryptionKey returns a base64 string of a 32-byte key suitable
// for ENCRYPTION_KEY. The bytes are deterministic so tests can assert
// on them without relying on randomness.
func validEncryptionKey() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db?sslmode=disable")
	t.Setenv("SESSION_SECRET", "session-secret")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("GOOGLE_WEB_CLIENT_ID", "web-client-id")
	t.Setenv("GOOGLE_EXT_CLIENT_ID", "ext-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("PUBLIC_BASE_URL", "https://api.example.test")
	t.Setenv("ENCRYPTION_KEY", validEncryptionKey())
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

// TestLoadAcceptsValidEncryptionKey covers Req 3.1: a valid 32-byte key
// is decoded and surfaced on Config.EncryptionKey.
func TestLoadAcceptsValidEncryptionKey(t *testing.T) {
	setRequiredEnv(t)
	cfg := Load()
	if got, want := len(cfg.EncryptionKey), 32; got != want {
		t.Fatalf("Config.EncryptionKey length = %d, want %d", got, want)
	}
}

// recoverPanicMessage runs fn, recovers a panic, and returns the panic
// payload as a string. It fails the test if fn does not panic.
func recoverPanicMessage(t *testing.T, fn func()) string {
	t.Helper()
	var msg string
	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			switch v := r.(type) {
			case string:
				msg = v
			case error:
				msg = v.Error()
			default:
				t.Fatalf("unexpected panic payload type %T: %v", v, v)
			}
		}()
		fn()
	}()
	if msg == "" {
		t.Fatalf("expected panic, got none")
	}
	return msg
}

// TestLoadPanicsWithoutEncryptionKey covers Req 3.2: an unset/empty
// ENCRYPTION_KEY must abort startup with the standard "missing env"
// message.
func TestLoadPanicsWithoutEncryptionKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ENCRYPTION_KEY", "")

	msg := recoverPanicMessage(t, func() { _ = Load() })
	if !strings.Contains(msg, "missing env: ENCRYPTION_KEY") {
		t.Fatalf("expected 'missing env: ENCRYPTION_KEY' in panic, got %q", msg)
	}
}

// TestLoadPanicsWithMalformedEncryptionKey covers Req 3.3: a value that
// is not valid base64 must abort startup with a base64-specific
// message and must not include the original key value.
func TestLoadPanicsWithMalformedEncryptionKey(t *testing.T) {
	setRequiredEnv(t)
	bogus := "!!not-base64!!"
	t.Setenv("ENCRYPTION_KEY", bogus)

	msg := recoverPanicMessage(t, func() { _ = Load() })
	if !strings.Contains(msg, "invalid env: ENCRYPTION_KEY") {
		t.Fatalf("expected 'invalid env: ENCRYPTION_KEY' in panic, got %q", msg)
	}
	if !strings.Contains(msg, "base64") {
		t.Fatalf("expected base64 hint in panic, got %q", msg)
	}
	// Req 3.5 / NFR 1.2: the panic message must not echo the bogus key.
	if strings.Contains(msg, bogus) {
		t.Fatalf("panic message must not contain the key value %q, got %q", bogus, msg)
	}
}

// TestLoadPanicsWithWrongLengthEncryptionKey covers Req 3.4: a base64
// value that decodes to anything other than 32 bytes must abort
// startup with a length-specific message.
func TestLoadPanicsWithWrongLengthEncryptionKey(t *testing.T) {
	setRequiredEnv(t)
	// 16 bytes -> AES-128 not AES-256, must be rejected.
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	t.Setenv("ENCRYPTION_KEY", short)

	msg := recoverPanicMessage(t, func() { _ = Load() })
	if !strings.Contains(msg, "invalid env: ENCRYPTION_KEY") {
		t.Fatalf("expected 'invalid env: ENCRYPTION_KEY' in panic, got %q", msg)
	}
	if !strings.Contains(msg, "32 bytes") {
		t.Fatalf("expected length hint in panic, got %q", msg)
	}
	// Req 3.5 / NFR 1.2: the panic message must not echo the key value.
	if strings.Contains(msg, short) {
		t.Fatalf("panic message must not contain the key value %q, got %q", short, msg)
	}
}

// TestLoadEncryptionKeyPanicMessageOmitsKeyValue is a focused
// double-check for Req 3.5 / NFR 1.2 across the malformed-and-wrong-length
// paths: under no circumstance should the panic include the raw key.
// We use a recognizable token in the env value so a substring match
// would fire if the message ever leaked it.
func TestLoadEncryptionKeyPanicMessageOmitsKeyValue(t *testing.T) {
	setRequiredEnv(t)
	leakCanary := "LEAK-CANARY-DO-NOT-LOG"
	t.Setenv("ENCRYPTION_KEY", leakCanary)

	msg := recoverPanicMessage(t, func() { _ = Load() })
	if strings.Contains(msg, leakCanary) {
		t.Fatalf("panic message leaked the env value: %q", msg)
	}
}
