package urlnorm

import (
	"errors"
	"testing"
)

func TestCanonicalize_Success(t *testing.T) {
	// Arrange
	cases := []struct {
		name     string
		raw      string
		expected string
	}{
		{"strip_utm", "https://example.com/page?utm_source=a&x=1", "https://example.com/page?x=1"},
		{"strip_utm_multiple", "https://example.com/page?utm_source=a&utm_medium=b&x=1", "https://example.com/page?x=1"},
		{"strip_fbclid", "https://example.com/page?fbclid=abc&x=1", "https://example.com/page?x=1"},
		{"strip_gclid", "https://example.com/page?gclid=abc&x=1", "https://example.com/page?x=1"},
		{"trim_trailing_slash", "https://example.com/page/", "https://example.com/page"},
		{"keep_root_slash", "https://example.com/", "https://example.com/"},
		{"http_absolute", "http://example.com", "http://example.com"},
		{"https_absolute", "https://example.com/", "https://example.com/"},
		{"uppercase_http_scheme", "HTTP://example.com", "http://example.com"},
		{"uppercase_https_scheme", "Https://example.com", "https://example.com"},
		{"sort_query_keys", "https://example.com/p?b=2&a=1", "https://example.com/p?a=1&b=2"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, gotHash, err := Canonicalize(tc.raw)

			// Assert
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("canonicalURL: got %q want %q", got, tc.expected)
			}
			if gotHash == "" {
				t.Fatalf("canonicalHash should not be empty for valid input")
			}
		})
	}
}

func TestCanonicalize_HashStableForSameInput(t *testing.T) {
	// Arrange
	raw := "https://example.com/page?utm_source=a&x=1"

	// Act
	url1, hash1, err1 := Canonicalize(raw)
	url2, hash2, err2 := Canonicalize(raw)

	// Assert
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v / %v", err1, err2)
	}
	if url1 != url2 {
		t.Fatalf("canonical URL not stable: %q vs %q", url1, url2)
	}
	if hash1 != hash2 {
		t.Fatalf("canonical hash not stable: %q vs %q", hash1, hash2)
	}
}

func TestCanonicalize_RejectsInvalidScheme(t *testing.T) {
	// Arrange: Requirement 1 AC-1 / AC-2
	cases := []struct {
		name string
		raw  string
	}{
		{"javascript_scheme", "javascript:alert(1)"},
		{"data_scheme", "data:text/plain;base64,SGVsbG8="},
		{"file_scheme", "file:///etc/passwd"},
		{"ftp_scheme", "ftp://example.com/file"},
		{"missing_scheme_with_host", "example.com/path"},
		{"relative_path", "/relative/path"},
		{"protocol_relative", "//example.com/path"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Act
			gotURL, gotHash, err := Canonicalize(tc.raw)

			// Assert
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.raw)
			}
			if !errors.Is(err, ErrInvalidScheme) {
				t.Fatalf("expected ErrInvalidScheme, got: %v", err)
			}
			if gotURL != "" {
				t.Fatalf("canonicalURL should be empty on rejection, got %q", gotURL)
			}
			if gotHash != "" {
				t.Fatalf("canonicalHash should be empty on rejection, got %q", gotHash)
			}
		})
	}
}

func TestCanonicalize_RejectsMissingHost(t *testing.T) {
	// Arrange: Requirement 2 AC-1 / AC-2
	cases := []struct {
		name string
		raw  string
	}{
		{"empty_string", ""},
		{"http_triple_slash_path", "http:///path"},
		{"https_no_host", "https://"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Act
			gotURL, gotHash, err := Canonicalize(tc.raw)

			// Assert
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.raw)
			}
			// Either ErrMissingHost (typical case) or ErrInvalidScheme (when scheme is also empty);
			// for these inputs we expect ErrMissingHost since scheme parses as http/https or empty
			// but the empty string and "http:///path" must be rejected by missing-host detection.
			// "https://" has https scheme but no host -> ErrMissingHost.
			// "" has no scheme and no host -> ErrInvalidScheme is acceptable.
			if !errors.Is(err, ErrMissingHost) && !errors.Is(err, ErrInvalidScheme) {
				t.Fatalf("expected ErrMissingHost or ErrInvalidScheme, got: %v", err)
			}
			if gotURL != "" {
				t.Fatalf("canonicalURL should be empty on rejection, got %q", gotURL)
			}
			if gotHash != "" {
				t.Fatalf("canonicalHash should be empty on rejection, got %q", gotHash)
			}
		})
	}
}

func TestCanonicalize_EmptyStringIsRejected(t *testing.T) {
	// Arrange: Requirement 2 AC-2 — explicitly verify empty string

	// Act
	gotURL, gotHash, err := Canonicalize("")

	// Assert
	if err == nil {
		t.Fatalf("expected error for empty input, got nil")
	}
	if !errors.Is(err, ErrInvalidScheme) && !errors.Is(err, ErrMissingHost) {
		t.Fatalf("expected sentinel error, got: %v", err)
	}
	if gotURL != "" || gotHash != "" {
		t.Fatalf("expected empty URL/hash on rejection, got %q / %q", gotURL, gotHash)
	}
}

func TestCanonicalize_HostMissingButHttpsScheme(t *testing.T) {
	// Arrange: Requirement 2 AC-1 — verify host-missing returns ErrMissingHost specifically
	// when the scheme is valid (http/https).
	cases := []string{
		"http:///path",
		"https://",
	}

	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			// Act
			_, _, err := Canonicalize(raw)

			// Assert
			if !errors.Is(err, ErrMissingHost) {
				t.Fatalf("expected ErrMissingHost for %q, got: %v", raw, err)
			}
		})
	}
}

func TestCanonicalize_ParseErrorIsDistinguishable(t *testing.T) {
	// Arrange: Requirement 3 AC-3 — url.Parse syntax errors must NOT be reported as the
	// scheme/host sentinel errors.
	// `://example.com` is one of the few inputs net/url's Parse rejects with a syntax error.
	raw := "://example.com"

	// Act
	gotURL, gotHash, err := Canonicalize(raw)

	// Assert
	if err == nil {
		t.Fatalf("expected error for %q, got nil", raw)
	}
	if errors.Is(err, ErrInvalidScheme) {
		t.Fatalf("parse error should not be reported as ErrInvalidScheme: %v", err)
	}
	if errors.Is(err, ErrMissingHost) {
		t.Fatalf("parse error should not be reported as ErrMissingHost: %v", err)
	}
	if gotURL != "" || gotHash != "" {
		t.Fatalf("expected empty URL/hash on rejection, got %q / %q", gotURL, gotHash)
	}
}

func TestCanonicalize_SentinelErrorsAreExported(t *testing.T) {
	// Arrange: Requirement 3 AC-1 / AC-4 — sentinels must be exported as package-level values.

	// Act / Assert
	if ErrInvalidScheme == nil {
		t.Fatalf("ErrInvalidScheme must be a non-nil exported sentinel error")
	}
	if ErrMissingHost == nil {
		t.Fatalf("ErrMissingHost must be a non-nil exported sentinel error")
	}
	if errors.Is(ErrInvalidScheme, ErrMissingHost) {
		t.Fatalf("ErrInvalidScheme and ErrMissingHost must be distinct sentinels")
	}
}
