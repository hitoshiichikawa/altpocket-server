package tag

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

func Normalize(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(norm.NFKC.String(trimmed))
}

// DisplayName returns the user-facing form of name with surrounding whitespace
// trimmed and Unicode forms folded via NFKC, while preserving letter case. It is
// the companion of Normalize: callers persist Normalize's output as the key
// (tags.normalized_name) and DisplayName's output as the human label
// (tags.name), so that chips can render the original "Go Lang" instead of the
// lowercase key "go lang" (Issue #115 / AC 1.3).
func DisplayName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	return norm.NFKC.String(trimmed)
}
