// Package urlnorm provides URL canonicalization for items stored in altpocket.
//
// Canonicalize は許可スキーム（http / https）と非空ホストを必須とし、
// トラッキングパラメータ（utm_* / fbclid / gclid）の除去・末尾スラッシュ整形・
// クエリキーの昇順ソートを行ったうえで、SHA-256 ダイジェストを返す。
package urlnorm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ErrInvalidScheme は入力 URL のスキームが http / https のいずれでもない場合、
// またはスキームが欠落している場合に Canonicalize から返されるエラーである。
//
// 呼び出し側は errors.Is(err, urlnorm.ErrInvalidScheme) でこの拒否理由を識別できる。
// url.Parse 由来の構文エラーはこの sentinel error にラップされない（区別可能）。
var ErrInvalidScheme = errors.New("urlnorm: invalid scheme")

// ErrMissingHost は入力 URL のホストが空である場合に Canonicalize から返される
// エラーである（例: "http:///path", "https://"）。
//
// 呼び出し側は errors.Is(err, urlnorm.ErrMissingHost) でこの拒否理由を識別できる。
var ErrMissingHost = errors.New("urlnorm: missing host")

var stripKeys = map[string]struct{}{
	"fbclid": {},
	"gclid":  {},
}

// Canonicalize は与えられた URL を正規化し、正規化後の URL と SHA-256 ダイジェスト
// （hex 文字列）を返す。
//
// 正規化の手順:
//  1. url.Parse で構文を検証する（失敗時は url.Parse 由来のエラーをラップして返す）。
//  2. スキームが http / https のいずれかであることを確認する。
//     違反時は ErrInvalidScheme をラップして返す。
//  3. Host が非空であることを確認する。
//     違反時は ErrMissingHost をラップして返す。
//  4. utm_* / fbclid / gclid を除去し、残りのクエリをキー昇順でソートする。
//  5. パスの末尾スラッシュを除去する（ただしルート "/" は維持する）。
//
// シグネチャ・戻り値の順序は固定（func Canonicalize(raw string) (canonicalURL,
// canonicalHash string, err error)）であり、後方互換のため変更しない。
//
// 失敗時は canonicalURL / canonicalHash ともに空文字列を返す。
func Canonicalize(raw string) (canonicalURL string, canonicalHash string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("urlnorm: parse %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", fmt.Errorf("urlnorm: scheme %q not allowed: %w", u.Scheme, ErrInvalidScheme)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("urlnorm: host is empty: %w", ErrMissingHost)
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
