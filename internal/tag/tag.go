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
