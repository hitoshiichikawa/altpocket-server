package urlnorm

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

var stripKeys = map[string]struct{}{
	"fbclid": {},
	"gclid":  {},
}

func Canonicalize(raw string) (canonicalURL string, canonicalHash string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") {
			q.Del(key)
			continue
		}
		if _, ok := stripKeys[lower]; ok {
			q.Del(key)
		}
	}
	if len(q) == 0 {
		u.RawQuery = ""
	} else {
		keys := make([]string, 0, len(q))
		for key := range q {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		values := url.Values{}
		for _, key := range keys {
			values[key] = q[key]
		}
		u.RawQuery = values.Encode()
	}
	if u.Path != "/" && strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimRight(u.Path, "/")
		if u.Path == "" {
			u.Path = "/"
		}
	}
	canonicalURL = u.String()
	h := sha256.Sum256([]byte(canonicalURL))
	canonicalHash = hex.EncodeToString(h[:])
	return canonicalURL, canonicalHash, nil
}
