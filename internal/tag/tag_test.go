package tag

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"  GoLang ", "golang"},
		{"ＡＢＣ", "abc"},
		{"", ""},
		{"  ", ""},
	}

	for _, tc := range cases {
		got := Normalize(tc.input)
		if got != tc.expected {
			t.Fatalf("Normalize(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"  Go Lang ", "Go Lang"},  // outer trim, case preserved
		{"ＧｏＬａｎｇ", "GoLang"}, // NFKC fullwidth -> halfwidth, case preserved
		{"", ""},
		{"  ", ""},
		{"Rust-Lang", "Rust-Lang"},
	}

	for _, tc := range cases {
		got := DisplayName(tc.input)
		if got != tc.expected {
			t.Fatalf("DisplayName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestDisplayNameNormalizePairing(t *testing.T) {
	// AC 1.3 invariant: DisplayName and Normalize must share the same trim+NFKC
	// preprocessing so that strings.EqualFold(DisplayName(x), Normalize(x)) holds
	// after lowercasing — i.e. they only diverge on case.
	cases := []string{"Go Lang", "  Rust Lang  ", "ＧｏＬａｎｇ", "TypeScript"}
	for _, tc := range cases {
		dn := DisplayName(tc)
		nm := Normalize(tc)
		if strings.ToLower(dn) != nm {
			t.Fatalf("pair(%q): lower(DisplayName=%q)=%q != Normalize=%q",
				tc, dn, strings.ToLower(dn), nm)
		}
	}
}
