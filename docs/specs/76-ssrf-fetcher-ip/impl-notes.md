# Implementation Notes — Issue #76 SSRF Fetcher IP Guard

## サマリ

Worker 側 fetcher に SSRF 防御を追加し、プライベート / ループバック / リンクローカル
（クラウドメタデータを含む）/ IPv6 ULA / IPv6 リンクローカル / unspecified / broadcast /
IPv4-mapped IPv6 経由のプライベート系 IP への HTTP 接続を遮断した。検査は ① URL 入口
（IP リテラル早期チェック）と ② TCP 接続直前（`http.Transport.DialContext` フック）
の 2 段階で行い、DNS rebinding TOCTOU にも耐える。

### 編集ファイル

| パス | 内容 |
|---|---|
| `internal/fetcher/ssrf.go`（新規） | `ErrBlockedIP` sentinel、`BlockedIPError` 型、`BlockedIPCategory`、`classifyIP` / `classifyIPv4` / `checkHostIPLiteral` / `guardedDialContext` |
| `internal/fetcher/fetcher.go`（編集） | `New()` で `http.Transport.DialContext = guardedDialContext(...)` を組み込み、`CheckRedirect` でリダイレクト先 IP リテラルを再検査、`Fetch()` 入口で URL の IP リテラル早期チェックを呼び出し |
| `internal/fetcher/ssrf_test.go`（新規） | `classifyIP` の table-driven test（IPv4 / IPv6 全レンジ + 公開 IP）、`checkHostIPLiteral` の URL レベルテスト、`guardedDialContext` の dial-time TOCTOU テスト、`BlockedIPError` の機密情報非漏洩テスト |
| `internal/fetcher/fetcher_test.go`（追記） | `Fetch()` が IP リテラル URL を `ErrBlockedIP` で拒否することと、`roundTripFunc` で Client 全置換した経路の挙動が破壊されないことを検証する 2 ケース |
| `cmd/worker/main.go`（編集） | `classifyFetchError` に `blocked_ip` ブランチを追加。`logFetchFailure` ヘルパで slog に `blocked_category`（loopback / private / link_local / unique_local / ipv4_mapped 等）を付加 |
| `cmd/worker/main_test.go`（追記） | `classifyFetchError` が `ErrBlockedIP` および wrap された `*BlockedIPError` を `blocked_ip` に分類することを検証 |

### 追加 sentinel / 理由コード

- Sentinel error: `fetcher.ErrBlockedIP` (`var ErrBlockedIP = errors.New("blocked_ip")`)
- 詳細エラー型: `fetcher.BlockedIPError`（`Category` / `IP` / `Host`、`Unwrap() = ErrBlockedIP`）
- カテゴリ列挙: `BlockedIPCategory` — `loopback` / `private` / `link_local` / `unique_local` /
  `unspecified` / `broadcast` / `multicast` / `ipv4_mapped` / `unparseable`
- Worker 理由コード文字列: `"blocked_ip"`（既存命名 `timeout` / `size_limit` / `redirect_limit` /
  `bad_status` / `no_content` と同じ snake_case 単語形式）

## AC ↔ テスト マッピング

| AC ID | 担保するテスト | 場所 |
|---|---|---|
| Req 1 AC-1（IPv4 ループバック） | `TestClassifyIP/ipv4_loopback_*`、`TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_loopback_url`、`TestGuardedDialContext_RejectsBlockedIPLiteralAddress/loopback_v4`、`TestFetchRejectsIPLiteralURLs` | `internal/fetcher/ssrf_test.go`、`fetcher_test.go` |
| Req 1 AC-2（IPv4 RFC1918） | `TestClassifyIP/ipv4_private_*`、`TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_private_url_with_port`、`TestGuardedDialContext_RejectsBlockedIPLiteralAddress/private_v4`、`TestClassifyIP/ipv4_cgnat_100_64`（CGNAT も private 扱い） | 同上 |
| Req 1 AC-3（IPv4 link-local + メタデータ） | `TestClassifyIP/ipv4_link_local_*`、`TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_link_local_metadata_url`、`TestGuardedDialContext_RejectsBlockedIPLiteralAddress/metadata_v4` | 同上 |
| Req 1 AC-4（IPv6 loopback / ULA / link-local） | `TestClassifyIP/ipv6_loopback`、`ipv6_ula_*`、`ipv6_link_local_*`、`TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv6_*`、`TestGuardedDialContext_RejectsBlockedIPLiteralAddress/loopback_v6`、`ula_v6` | 同上 |
| Req 1 AC-5（unspecified / broadcast / IPv4-mapped IPv6） | `TestClassifyIP/ipv4_unspecified_0_0_0_0`、`ipv6_unspecified_double_colon`、`ipv4_broadcast`、`ipv4_mapped_*`、`TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_unspecified_url`、`ipv4_broadcast_url`、`ipv4_mapped_loopback_url` | 同上 |
| Req 1 AC-6（IP リテラル URL の早期拒否） | `TestCheckHostIPLiteral_BlocksIPLiteralURLs`（全 9 ケース）、`TestFetchRejectsIPLiteralURLs`（全 9 ケース） | 同上 |
| Req 1 AC-7（複数解決結果のいずれかが禁止レンジ） | `guardedDialContext` のホスト名解決パス（`for _, ipa := range ips` ループ）。すべての候補を順に検査し、1 つでも禁止レンジなら拒否する実装で担保。専用の DNS モックテストは未追加（OQ-1 の通り stdlib `net.Resolver` を簡易差し替えできないため。動作はコードレビューでのコード読みで保証） | `internal/fetcher/ssrf.go:250-254` |
| Req 1 AC-8（公開 IP は許可） | `TestClassifyIP/ipv4_public_8_8_8_8`、`ipv4_public_1_1_1_1`、`ipv6_public_google`、`ipv4_mapped_public`、`TestCheckHostIPLiteral_AllowsPublicIPLiterals`、`TestCheckHostIPLiteral_AllowsHostnames` | `internal/fetcher/ssrf_test.go` |
| Req 2 AC-1（TCP 接続直前の IP 再検査） | `TestGuardedDialContext_RejectsBlockedIPLiteralAddress`（全 5 ケース） | `internal/fetcher/ssrf_test.go` |
| Req 2 AC-2（DNS rebinding TOCTOU） | `TestGuardedDialContext_RebindingResolverReturnsPrivateIP` — 実際にネットワークレイヤを介さずに「dial 時に渡された address を guard が検査する」挙動を検証 | 同上 |
| Req 2 AC-3（リダイレクト追従時の再検査） | `Fetcher.New()` の `CheckRedirect` で `checkHostIPLiteral` を再呼び出し（IP リテラル経路）。ホスト名経由のリダイレクトは `DialContext` で再度検査されるため、リダイレクト先が rebind されてもブロックされる。挙動上、`TestFetchRejectsIPLiteralURLs` でリダイレクト先と同等の URL 検査が走ることを間接的に確認 | `internal/fetcher/fetcher.go:60-72` |
| Req 2 AC-4（DialContext 経路で都度検査） | `TestGuardedDialContext_RejectsBlockedIPLiteralAddress`、`TestGuardedDialContext_RebindingResolverReturnsPrivateIP` | `internal/fetcher/ssrf_test.go` |
| Req 3 AC-1（識別可能 sentinel） | `TestCheckHostIPLiteral_BlocksIPLiteralURLs`（`errors.Is(err, ErrBlockedIP)` と `errors.As(err, &be)` の両方を検証）、`TestFetchRejectsIPLiteralURLs`、`TestGuardedDialContext_RejectsBlockedIPLiteralAddress` | `internal/fetcher/ssrf_test.go`、`fetcher_test.go` |
| Req 3 AC-2（Worker 理由コード追加） | `TestClassifyFetchErrorBlockedIP/sentinel_error`、`wrapped_blocked_ip_error` | `cmd/worker/main_test.go` |
| Req 3 AC-3（slog 出力に item ID と category） | `logFetchFailure` の実装で `errors.As` 経由の `BlockedIPError` 検出 → `blocked_category` フィールド付与。`go test` での実行ログを直接 assert するテストは未追加（slog ハンドラのキャプチャに helper が必要なため）。実装読みで担保 | `cmd/worker/main.go:150-162` |
| Req 3 AC-4（機密情報非漏洩） | `TestBlockedIPError_DoesNotLeakURLPath`、`TestCheckHostIPLiteral_BlocksIPLiteralURLs` 内の `if strings.Contains(be.Error(), "/admin") ...` assert | `internal/fetcher/ssrf_test.go` |
| Req 4 AC-1（IP 範囲判定の独立テスト） | `TestClassifyIP`（24 ケースの table-driven） | `internal/fetcher/ssrf_test.go` |
| Req 4 AC-2（既存 roundTripFunc テスト互換） | 既存テスト 7 件（`TestFetchSuccess` 等）が緑のまま、`TestFetchAllowsRoundTripFuncOverride`（追加） | `internal/fetcher/fetcher_test.go` |
| Req 4 AC-3（SSRF 拒否動作の最低 1 ケース） | `TestFetchRejectsIPLiteralURLs`、`TestGuardedDialContext_RejectsBlockedIPLiteralAddress`、`TestGuardedDialContext_RebindingResolverReturnsPrivateIP` | 同上 |
| Req 4 AC-4（公開 IP 接続の正常系維持） | `TestFetchAllowsRoundTripFuncOverride`（モックで成功 200 を返す）、既存 `TestFetchSuccess` ほか | `internal/fetcher/fetcher_test.go` |
| NFR 1.1（既定有効・opt-in 不要） | `New()` のすべての呼び出しで guard が組み込まれる。設定オプションなし | `internal/fetcher/fetcher.go:39-75` |
| NFR 1.2（deny-by-default） | `classifyIP(nil)` が `categoryUnparseableIP` を返し、`guardedDialContext` で nil IP もエラー扱い | `TestClassifyIP_NilReturnsUnparseable`、`internal/fetcher/ssrf.go:71-73` |
| NFR 1.3（ログ機密情報禁止） | `logFetchFailure` は URL を一切ログに出さず `item_id` / `reason` / `blocked_category` のみ。`TestBlockedIPError_DoesNotLeakURLPath` で error 文字列も path/query を含まないことを検証 | 同上 |
| NFR 2.1（< 5ms オーバーヘッド） | 公開 IP パスでは `classifyIP` が定数時間で抜け、`base.DialContext` に委譲するのみ。実測ベンチは未追加（必要なら別 Issue） | 設計上担保 |
| NFR 2.2（拒否を別カテゴリで集計） | `"blocked_ip"` 理由コードでユニーク化、ログには `blocked_category` も付加 | `cmd/worker/main.go:140-162` |
| NFR 3.1（既存 fetch 挙動互換） | `Transport` の他項目は `http.DefaultTransport` 相当に揃え、タイムアウト・リダイレクト上限・サイズ制限は変更なし。既存テスト 7 件緑 | `internal/fetcher/fetcher.go:39-75` |
| NFR 3.2（既存テスト破壊なし） | `go test ./...` 全パッケージ green | 後述「実行結果」 |

## Open Questions への回答

### OQ-1（テスト用 loopback 許可機構の要否）

**採用方針: (b) Client 差し替え**を採用した。理由:

- 既存テストが `Fetcher.Client = &http.Client{Transport: roundTripFunc(...)}` パターンで
  Client を完全置換する形式を取っており、SSRF guard は `New()` がデフォルトで生成する
  Transport の `DialContext` に組み込まれているため、Client 差し替えで自動的に guard が
  バイパスされる
- 環境変数による許可フラグ（option (a)）は導入せず、`NFR 1.1`（opt-in にしない）と
  「拒否がデフォルト・許可は明示」（`NFR 1.2`）の方針を維持
- 依存注入による `AllowedIPChecker` 差し替え（option (c)）も導入せず、APIサーフェスを
  最小化
- ただし `Fetch()` の入口で `checkHostIPLiteral` が走るため、IP リテラル URL を直接渡す
  loopback テスト（例: `httptest.NewServer` で生成された `http://127.0.0.1:xxxx/` URL
  を渡すケース）は早期拒否される。将来そうした integration test が必要になった場合は
  `Fetcher.SkipIPCheck bool` のような escape hatch を追加する Issue を別途切ること

### OQ-2（DB 上の理由コード文字列）

**採用値: `"blocked_ip"`**。既存 `cmd/worker/main.go` の命名（`timeout`, `size_limit`,
`redirect_limit`, `bad_status`, `no_content` — すべて snake_case の動詞または名詞句）に
合わせた。`ssrf_blocked` は技術用語が表に出るため避け、`private_ip` はループバック等を
カバーしない狭義語のため避けた。

### OQ-3（ログ拒否カテゴリ粒度）

`BlockedIPCategory` の列挙値で粒度を確定させた:

- `loopback`、`private`、`link_local`、`unique_local`（IPv6 ULA）、`unspecified`、
  `broadcast`、`multicast`、`ipv4_mapped`（::ffff:バイパス検出）、`unparseable`

最低限の要件（「拒否されたこと」と「IP の所属レンジが大分類で何か」が分かる）は満たし、
さらに ULA / ipv4_mapped を別ラベルにすることで「IPv6 経由のバイパス試行」がログ集計で
浮き上がるようにした。粒度を統合したい場合は将来別 Issue で `category_group` のような
集約フィールドを追加する余地がある。

## 確認事項

- **DialContext のホスト名解決パス（`net.Resolver.LookupIPAddr` 経由）の単体テスト未追加**:
  Go stdlib の `net.Resolver` をモック差し替える簡潔な方法がなく、`Resolver.Dial` フック
  を使う場合は実 DNS protocol を喋る必要がある。実装は単純な「all candidate IPs を classify
  する for ループ」のみで、各 IP の判定は `TestClassifyIP` で網羅済み。レビュアー判断で
  追加テストが必要なら Issue を切ってください
- **slog 出力内容の assert テスト未追加**: Req 3 AC-3 の「item ID と blocked_category が
  ログに出る」は `logFetchFailure` の実装読みで担保している。`slog.NewJSONHandler` を
  バッファに付け替える test helper を `internal/logger` に追加する選択肢もあるが、本 Issue
  のスコープを超えるため見送った
- **CGNAT 100.64.0.0/10 を private 扱いにした**: `net.IP.IsPrivate()` の Go 1.25 時点の
  実装で CGNAT が含まれているため `classifyIP` の `IsPrivate()` 分岐で吸収される。
  `classifyIPv4` の明示分岐は冗長だが、stdlib の挙動が将来変わった場合の安全網として残した
- **`http.Client.Timeout` を二重に持つ形になった**: `Client.Timeout = 10s` と
  `Dialer.Timeout = 10s` は別の意味（前者はリクエスト全体、後者は TCP 接続のみ）なので
  二重設定は意図通り。読みづらいなら定数化する余地あり

## 残課題

- `Fetcher.SkipIPCheck` のような escape hatch の追加是非（OQ-1 で見送り、必要なら別 Issue）
- `DialContext` のホスト名解決パスの DNS モックテスト追加（OQ-1 で見送り）
- slog 出力内容の assert テスト追加（NFR 1.3 / Req 3 AC-3 の machine-checked 担保）
- ベンチマーク（NFR 2.1 の 5ms 未満を実測で確認）

## 実行結果

```bash
$ go test ./internal/fetcher/...
ok  	altpocket/internal/fetcher	0.002s

$ go test ./...
ok  	altpocket/cmd/worker	0.002s
ok  	altpocket/internal/auth	(cached)
ok  	altpocket/internal/config	(cached)
ok  	altpocket/internal/fetcher	(cached)
ok  	altpocket/internal/mcpserver	(cached)
ok  	altpocket/internal/ratelimit	(cached)
ok  	altpocket/internal/server	(cached)
ok  	altpocket/internal/store	(cached)
ok  	altpocket/internal/tag	(cached)
ok  	altpocket/internal/ui	(cached)
ok  	altpocket/internal/urlnorm	(cached)
（cmd/api, internal/db, internal/logger は no test files）

$ golangci-lint run
（未インストールのため未実行 — CI 側で検証）

$ gofmt -l internal/fetcher/ cmd/worker/
（差分なし）

$ go vet ./...
（差分なし）
```

## Feature Flag Protocol

`CLAUDE.md` で `**採否**: opt-out` のため、本機能には flag を導入していない（NFR 1.1
要件と整合）。
