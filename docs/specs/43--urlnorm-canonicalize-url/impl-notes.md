# Impl Notes — Issue #43 urlnorm.Canonicalize がスキーム無し URL を通す

## 実装方針サマリ

`internal/urlnorm/urlnorm.go` の `Canonicalize` に対して、`url.Parse` 成功後に以下 2 段の
検証を追加した。両方ともライブラリ層の検証であり、I/O は発生しない（NFR 3 に適合）。

1. `strings.ToLower(u.Scheme)` が `http` または `https` のいずれでもない場合
   `ErrInvalidScheme` をラップして返す（Requirement 1 / AC-1, AC-2）
2. `u.Host` が空文字列の場合 `ErrMissingHost` をラップして返す（Requirement 2 / AC-1, AC-2）

スキーム小文字化は `net/url` 標準挙動どおり受理させる目的（Requirement 1 AC-3）であり、
正規化済み `u.String()` は `net/url` が小文字スキームに整える挙動に従う。

### sentinel error の分割方針

呼び出し側（`internal/server` 等）が拒否理由を **個別に** 識別できるよう、単一 `ErrInvalidURL`
ではなく以下 2 つに分けた:

- `ErrInvalidScheme` — `urlnorm: invalid scheme`
- `ErrMissingHost` — `urlnorm: missing host`

それぞれ godoc コメントで「呼び出し側が `errors.Is` で識別できる」旨を明示し、公開 API
（パッケージ doc の最初の段落）として位置づけた（Requirement 3 AC-4）。

`url.Parse` 由来のエラーは `fmt.Errorf("urlnorm: parse %q: %w", raw, err)` で `url.Parse`
そのもののエラーをラップするため、上記 2 つの sentinel には一致しない（Requirement 3 AC-3）。
これを `TestCanonicalize_ParseErrorIsDistinguishable` で具体的に検証した。

エラーメッセージには `raw`（URL 文字列）以外の秘匿情報（Cookie / token 等）を含めないため、
NFR 2 を満たす。

### 失敗時の戻り値

仕様どおり `canonicalURL`, `canonicalHash` の両方を空文字列で返す。これは複数のエラーケース
テストでアサート済み（Requirement 1 AC-1, Requirement 2 AC-1 / AC-2）。

### 既存正常系の互換性

`url.Parse` 後の正規化ロジック（クエリ整形・末尾スラッシュ整形）は touch しておらず、
動作差分なし（Requirement 4 / NFR 1）。`TestCanonicalize_Success` で utm 除去、fbclid 除去、
gclid 除去、末尾スラッシュ整形、ルート `/` 維持、クエリキー昇順ソート、HTTP/HTTPS 大文字
スキーム受理、ハッシュ非空をいずれも検証している。

## テスト一覧と AC マッピング

| テスト関数 / サブテスト | 対象 AC |
| --- | --- |
| `TestCanonicalize_Success/strip_utm` | Requirement 4 AC-2 / Requirement 5 AC-4 |
| `TestCanonicalize_Success/strip_utm_multiple` | Requirement 4 AC-2 / Requirement 5 AC-4 |
| `TestCanonicalize_Success/strip_fbclid` | Requirement 4 AC-2 / Requirement 5 AC-4 |
| `TestCanonicalize_Success/strip_gclid` | Requirement 4 AC-2 / Requirement 5 AC-4 |
| `TestCanonicalize_Success/trim_trailing_slash` | Requirement 4 AC-3 / Requirement 5 AC-4 |
| `TestCanonicalize_Success/keep_root_slash` | Requirement 4 AC-3 / Requirement 5 AC-4 |
| `TestCanonicalize_Success/http_absolute` | Requirement 4 AC-1 |
| `TestCanonicalize_Success/https_absolute` | Requirement 4 AC-1 |
| `TestCanonicalize_Success/uppercase_http_scheme` | Requirement 1 AC-3 |
| `TestCanonicalize_Success/uppercase_https_scheme` | Requirement 1 AC-3 |
| `TestCanonicalize_Success/sort_query_keys` | NFR 1.2（クエリキー昇順ソート） |
| `TestCanonicalize_HashStableForSameInput` | Requirement 4 AC-1（ハッシュ安定性） |
| `TestCanonicalize_RejectsInvalidScheme/javascript_scheme` | Requirement 1 AC-1 / Requirement 5 AC-1 |
| `TestCanonicalize_RejectsInvalidScheme/data_scheme` | Requirement 1 AC-1 |
| `TestCanonicalize_RejectsInvalidScheme/file_scheme` | Requirement 1 AC-1 |
| `TestCanonicalize_RejectsInvalidScheme/ftp_scheme` | Requirement 1 AC-1 |
| `TestCanonicalize_RejectsInvalidScheme/missing_scheme_with_host` | Requirement 1 AC-2 / Requirement 5 AC-1 |
| `TestCanonicalize_RejectsInvalidScheme/relative_path` | Requirement 1 AC-2 / Requirement 5 AC-1 |
| `TestCanonicalize_RejectsInvalidScheme/protocol_relative` | Requirement 1 AC-2 |
| `TestCanonicalize_RejectsMissingHost/empty_string` | Requirement 2 AC-2 / Requirement 5 AC-2 |
| `TestCanonicalize_RejectsMissingHost/http_triple_slash_path` | Requirement 2 AC-1 / Requirement 5 AC-2 |
| `TestCanonicalize_RejectsMissingHost/https_no_host` | Requirement 2 AC-1 / Requirement 5 AC-2 |
| `TestCanonicalize_EmptyStringIsRejected` | Requirement 2 AC-2 |
| `TestCanonicalize_HostMissingButHttpsScheme/http:///path` | Requirement 2 AC-1 |
| `TestCanonicalize_HostMissingButHttpsScheme/https://` | Requirement 2 AC-1 |
| `TestCanonicalize_ParseErrorIsDistinguishable` | Requirement 3 AC-3 / Requirement 5 AC-3 |
| `TestCanonicalize_SentinelErrorsAreExported` | Requirement 3 AC-1 / Requirement 3 AC-4 |

Requirement 3 AC-2（`errors.Is` で sentinel error と一致）は
`TestCanonicalize_RejectsInvalidScheme` および `TestCanonicalize_RejectsMissingHost` の
全サブテストで検証している（各サブテストが `errors.Is(err, ErrInvalidScheme)` /
`errors.Is(err, ErrMissingHost)` を直接アサートしている）。

NFR 2.2（エラーメッセージに秘匿情報を含めない）はメッセージ文言を `urlnorm: invalid scheme`,
`urlnorm: missing host`, `urlnorm: parse %q` に固定し Cookie/token を含めないことで担保。

## 呼び出し側に与える影響の確認結果

`urlnorm.Canonicalize` の呼び出し箇所は repo 内で 1 箇所のみ:

- `internal/server/server.go:1362` `(*Server).createItem`

該当箇所は `err != nil` をそのまま `errInvalidURL` に変換しているため、本変更で sentinel error
を返すようになっても挙動互換性は完全に維持される（Out of Scope 通り API レスポンスの仕様は
変えない）。`errInvalidURL` は同パッケージ内で定義され HTTP 400 にマップされており、上位
ハンドラ（`server.go:513`, `server.go:1124`）の `errors.Is(err, errInvalidURL)` 経路は不変。

## 実行確認

- `go test ./...` → 全パッケージ通過（`internal/urlnorm` を含む）
- `go vet ./...` → エラーなし
- `go build ./...` → エラーなし
- `golangci-lint run` → 当該実行環境に `golangci-lint` バイナリが入っていなかったため
  ローカル実行不可。CI（`.github/workflows/ci.yml`）側で `golangci-lint v2.11.4` が走るため、
  そちらでの検証に依存する。`go vet` ベースの静的検査は通過済み。

## 確認事項

- なし。要件定義（requirements.md）の Open Questions も「なし」で確定しており、scope 内で
  実装可能だった。Out of Scope（IDN 変換 / DNS 解決 / 既存データバックフィル / 呼び出し側
  エラー応答変更等）には踏み込んでいない。

## 直近 commit

```
db8eb08 fix(urlnorm): reject invalid scheme and missing host in Canonicalize
af6ffa9 Merge pull request #122 from hitoshiichikawa/claude/issue-114-impl--debounce-url
83f8114 docs(review): add reviewer notes for #114
```
