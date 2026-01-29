package tag

import "testing"

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
