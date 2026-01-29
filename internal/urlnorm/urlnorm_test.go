package urlnorm

import "testing"

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		expected   string
	}{
		{"strip_utm", "https://example.com/page?utm_source=a&x=1", "https://example.com/page?x=1"},
		{"strip_fbclid", "https://example.com/page?fbclid=abc&x=1", "https://example.com/page?x=1"},
		{"strip_gclid", "https://example.com/page?gclid=abc&x=1", "https://example.com/page?x=1"},
		{"trim_trailing_slash", "https://example.com/page/", "https://example.com/page"},
		{"keep_root_slash", "https://example.com/", "https://example.com/"},
	}

	for _, tc := range cases {
		got, _, err := Canonicalize(tc.raw)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if got != tc.expected {
			t.Fatalf("%s: got %s want %s", tc.name, got, tc.expected)
		}
	}
}
