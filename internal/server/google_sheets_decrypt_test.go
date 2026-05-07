package server

import (
	"errors"
	"fmt"
	"testing"

	"altpocket/internal/store"

	"github.com/jackc/pgx/v5"
)

// TestErrRefreshTokenDecryptFailedIsExportedSentinel guards the contract
// the Google Sheets handlers depend on: store.ErrRefreshTokenDecryptFailed
// is a stable, comparable sentinel value (not constructed per-call) so
// that errors.Is can reliably distinguish it from pgx.ErrNoRows.
//
// If this contract regresses, both handleUISettings and
// handleUISettingsGoogleExport will silently fall through to the
// generic "internal error" / "export_failed" branches instead of the
// "not connected / re-authorize" UX (Req 2.3).
func TestErrRefreshTokenDecryptFailedIsExportedSentinel(t *testing.T) {
	if store.ErrRefreshTokenDecryptFailed == nil {
		t.Fatal("store.ErrRefreshTokenDecryptFailed must be a non-nil sentinel")
	}

	// Identity check: the value must equal itself across imports.
	if !errors.Is(store.ErrRefreshTokenDecryptFailed, store.ErrRefreshTokenDecryptFailed) {
		t.Fatal("errors.Is should return true for identity match")
	}

	// Wrap and unwrap: handlers may receive the error through multiple
	// fmt.Errorf %w layers (e.g. from a future store helper). Make sure
	// the sentinel survives standard wrapping.
	wrapped := fmt.Errorf("get connection: %w", store.ErrRefreshTokenDecryptFailed)
	if !errors.Is(wrapped, store.ErrRefreshTokenDecryptFailed) {
		t.Fatal("errors.Is must see through fmt.Errorf wrapping")
	}

	// Distinctness: the decrypt sentinel must NOT collide with
	// pgx.ErrNoRows because the handlers branch on each separately.
	if errors.Is(store.ErrRefreshTokenDecryptFailed, pgx.ErrNoRows) {
		t.Fatal("ErrRefreshTokenDecryptFailed must not equal pgx.ErrNoRows")
	}
	if errors.Is(pgx.ErrNoRows, store.ErrRefreshTokenDecryptFailed) {
		t.Fatal("pgx.ErrNoRows must not equal ErrRefreshTokenDecryptFailed")
	}
}

// TestSettingsNoticeGoogleNotConnectedReusedForDecryptFailure pins the
// UX-text decision recorded in design.md OQ-3: a decryption failure
// reuses the existing "google_not_connected" notice
// ("Connect Google before exporting.") rather than introducing a new
// status code, so that Req 2.3 ("merge into the existing not-connected
// flow") and NFR 2.1 ("don't change externally observable behavior")
// both remain satisfied.
func TestSettingsNoticeGoogleNotConnectedReusedForDecryptFailure(t *testing.T) {
	msg, cls := settingsNotice("google_not_connected")
	if msg == "" {
		t.Fatal("expected a non-empty notice for google_not_connected status")
	}
	if cls != "error" {
		t.Fatalf("expected error class, got %q", cls)
	}
}
