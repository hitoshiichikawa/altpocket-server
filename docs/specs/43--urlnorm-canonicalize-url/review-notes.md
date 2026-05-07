# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-04-30T17:39:37Z -->

## Reviewed Scope

- Branch: claude/issue-43-impl--urlnorm-canonicalize-url
- HEAD commit: e900c58bc47e3ff79651f4bc7f54b2be1c0488cd
- Compared to: main..HEAD
- Changed files (4): `internal/urlnorm/urlnorm.go`, `internal/urlnorm/urlnorm_test.go`,
  `docs/specs/43--urlnorm-canonicalize-url/requirements.md`,
  `docs/specs/43--urlnorm-canonicalize-url/impl-notes.md`
- Feature Flag Protocol: CLAUDE.md `## Feature Flag Protocol` の `**採否**: opt-out` を確認。
  flag 観点（boundary 細目）は適用しない（通常 3 カテゴリ判定のみ）。
- 再実行検証: `go test ./internal/urlnorm/...` を reviewer 環境で実行し PASS（`-v` で
  サブテスト個別 PASS も確認）。

## Verified Requirements

- 1.1（非 http/https スキーム拒否）— `TestCanonicalize_RejectsInvalidScheme` の
  `javascript_scheme` / `data_scheme` / `file_scheme` / `ftp_scheme` サブテストが
  `ErrInvalidScheme` 一致と `canonicalURL`/`canonicalHash` 空をアサート
  （`internal/urlnorm/urlnorm_test.go:74-103`）
- 1.2（スキーム欠落拒否）— 同テストの `missing_scheme_with_host` / `relative_path` /
  `protocol_relative` サブテスト（`internal/urlnorm/urlnorm_test.go:78-80`）
- 1.3（大文字スキーム受理）— `TestCanonicalize_Success/uppercase_http_scheme` /
  `uppercase_https_scheme`（`internal/urlnorm/urlnorm_test.go:23-24`）。
  実装側 `strings.ToLower(u.Scheme)` 比較で対応（`internal/urlnorm/urlnorm.go:57`）
- 2.1（host 空・スキーム正当）— `TestCanonicalize_HostMissingButHttpsScheme` で
  `http:///path` と `https://` の双方が `ErrMissingHost` 一致を直接アサート
  （`internal/urlnorm/urlnorm_test.go:163-183`）。同義の `RejectsMissingHost`
  サブテストも併存
- 2.2（空文字列拒否）— `TestCanonicalize_EmptyStringIsRejected`
  （`internal/urlnorm/urlnorm_test.go:145-161`）。`RejectsMissingHost/empty_string`
  も追加で網羅
- 3.1（公開 sentinel error 提供）— `ErrInvalidScheme` / `ErrMissingHost` を package
  level の `errors.New(...)` として export（`internal/urlnorm/urlnorm.go:23, 29`）。
  `TestCanonicalize_SentinelErrorsAreExported` で非 nil と相互 distinct を検証
- 3.2（`errors.Is` 一致）— `TestCanonicalize_RejectsInvalidScheme` の各サブテスト
  （`internal/urlnorm/urlnorm_test.go:93`）および
  `TestCanonicalize_HostMissingButHttpsScheme` の各サブテスト
  （`internal/urlnorm/urlnorm_test.go:178`）でループ内 `errors.Is` を直接アサート
- 3.3（parse 由来エラーは sentinel と区別可能）—
  `TestCanonicalize_ParseErrorIsDistinguishable` が `://example.com` で url.Parse 由来
  エラーを発生させ、`errors.Is(err, ErrInvalidScheme)` / `errors.Is(err, ErrMissingHost)`
  両方が false であることをアサート（`internal/urlnorm/urlnorm_test.go:185-207`）。
  実装側は `fmt.Errorf("urlnorm: parse %q: %w", raw, err)` で `url.Parse` のエラーのみ
  をラップ（`internal/urlnorm/urlnorm.go:55`）
- 3.4（godoc で公開 API 明示）— `internal/urlnorm/urlnorm.go:18-23, 25-29` に sentinel
  名・型・呼び出し側の `errors.Is` 用法を明記
- 4.1（既存正常系の不変）— `TestCanonicalize_Success/http_absolute` /
  `https_absolute` / `TestCanonicalize_HashStableForSameInput`
- 4.2（utm/fbclid/gclid 除去）— `strip_utm` / `strip_utm_multiple` / `strip_fbclid` /
  `strip_gclid`
- 4.3（末尾スラッシュ整形 / ルート維持）— `trim_trailing_slash` / `keep_root_slash`
- 4.4（シグネチャ不変）— `func Canonicalize(raw string) (canonicalURL string,
  canonicalHash string, err error)`（`internal/urlnorm/urlnorm.go:52`）。
  呼び出し側 `internal/server/server.go:1362` を grep で確認、変更なし
- 5.1〜5.4（テスト追加・既存保持）— 上記すべてのテストが新規追加、既存の正常系
  サブテストは削除・弱体化されていない（`go test -v` で PASS 確認）
- NFR 1.1（公開シグネチャ不変）— 4.4 と同根拠
- NFR 1.2（既存正規化ルール不変）— 実装 diff で `q := u.Query()` 以降のロジックに
  変更なし。`sort_query_keys` サブテストでクエリキー昇順ソート維持を確認
- NFR 2.1 / 2.2（エラーメッセージに秘匿情報を含めない）— 文言は `urlnorm: invalid
  scheme` / `urlnorm: missing host` / `urlnorm: parse %q`、Cookie / token を扱う経路
  なし。`raw` 自体は呼び出し側が渡す URL であり Cookie/token とは別領分（要件本文と
  整合）
- NFR 3.1（追加 I/O なし）— 実装は文字列処理 + `net/url.Parse` のみ。DNS 解決・
  ネットワーク・ファイル I/O いずれも追加されていない

## Boundary Check

- 変更ファイルは `internal/urlnorm/` の 2 ファイルと `docs/specs/43--*` 配下のみ。
  本 Issue は tasks.md / design.md を持たない（PM/Developer のみ起動経路）が、
  Issue 本文の修正範囲（`internal/urlnorm/urlnorm.go` および対応テスト）と完全一致
- `Canonicalize` の呼び出し側 `internal/server/server.go:1362` は touch されておらず、
  既存の `errInvalidURL` 変換経路は不変（impl-notes.md の確認結果と一致）
- `panic` / 起動時 fail-fast 以外の `panic` 使用なし、外部 I/O なし、ハンドラ層の
  境界違反なし

## Findings

なし

## Summary

要件 1〜5 / NFR 1〜3 の全 numeric ID に対して、新規テストおよび既存テストでの
カバーを確認。sentinel error は `ErrInvalidScheme` / `ErrMissingHost` の 2 種に分割
公開され、`errors.Is` での識別および parse 由来エラーとの区別もテストで担保されて
いる。シグネチャ・既存正規化ロジックは変更なく、呼び出し側 `internal/server` の
互換性も維持。境界違反・panic・追加 I/O いずれも検出されず、`go test
./internal/urlnorm/...` は reviewer 環境で PASS。

RESULT: approve
