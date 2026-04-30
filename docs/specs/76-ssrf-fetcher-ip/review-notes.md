# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-05-01T00:00:00Z -->

## Reviewed Scope

- Branch: claude/issue-76-impl-ssrf-fetcher-ip
- HEAD commit: 39f48609a52d70d853df12290ed705d373c7e3fe
- Compared to: main..HEAD
- Feature Flag Protocol: `**採否**: opt-out`（CLAUDE.md L303）→ 通常 3 カテゴリのみで判定
- tasks.md / design.md: 存在しない（Architect 不起動の Issue）。Boundary は requirements.md の
  Out of Scope セクションと Issue #76 の対象範囲を境界として評価

## Verified Requirements

### Requirement 1（プライベート／内部ネットワーク向け接続の遮断）

- 1.1（IPv4 ループバック） — `TestClassifyIP/ipv4_loopback_*`（3 ケース）、
  `TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_loopback_url`、
  `TestGuardedDialContext_RejectsBlockedIPLiteralAddress/loopback_v4`、
  `TestFetchRejectsIPLiteralURLs[http://127.0.0.1/]`
- 1.2（IPv4 RFC1918 private） — `TestClassifyIP/ipv4_private_*`（4 ケース）、
  `TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_private_url_with_port`、
  `TestGuardedDialContext_RejectsBlockedIPLiteralAddress/private_v4`、
  `TestFetchRejectsIPLiteralURLs[http://10.0.0.1/admin]`
- 1.3（IPv4 link-local + EC2/GCP メタデータ 169.254.169.254） — `TestClassifyIP/ipv4_link_local_*`、
  `TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_link_local_metadata_url`、
  `TestGuardedDialContext_RejectsBlockedIPLiteralAddress/metadata_v4`、
  `TestFetchRejectsIPLiteralURLs[http://169.254.169.254/latest/meta-data/]`
- 1.4（IPv6 loopback ::1 / ULA fc00::/7 / link-local fe80::/10） — `TestClassifyIP/ipv6_loopback`、
  `ipv6_ula_fc00`、`ipv6_ula_fd00`、`ipv6_link_local_fe80`、対応する
  `TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv6_*`、`TestGuardedDialContext_*/loopback_v6`、`/ula_v6`
- 1.5（unspecified / broadcast / IPv4-mapped IPv6） —
  `TestClassifyIP/ipv4_unspecified_0_0_0_0`、`ipv6_unspecified_double_colon`、`ipv4_broadcast`、
  `ipv4_mapped_loopback`、`ipv4_mapped_private`、`ipv4_mapped_link_local`、対応する
  `TestCheckHostIPLiteral_BlocksIPLiteralURLs/ipv4_unspecified_url`、`ipv4_broadcast_url`、
  `ipv4_mapped_loopback_url`（`categoryIPv4Mapped` を別ラベルで surface）
- 1.6（IP リテラル URL の DNS 解決前拒否） — `TestCheckHostIPLiteral_BlocksIPLiteralURLs`（9 ケース）、
  `TestFetchRejectsIPLiteralURLs`（9 ケース）。`Fetch()` 入口（fetcher.go:81-83）で
  `checkHostIPLiteral` を呼ぶため `http.NewRequestWithContext` 前に拒否される
- 1.7（複数解決結果のいずれかが禁止レンジ） — `internal/fetcher/ssrf.go:250-254` の
  `for _, ipa := range ips { if cat := classifyIP(ipa.IP); cat != "" { return ... } }` ループで実装。
  単一 IP の判定は `TestClassifyIP` で網羅済みで、ループ実装は単純な「いずれか 1 つでも禁止なら拒否」。
  DNS モック専用テストは未追加だが、stdlib `net.Resolver.LookupIPAddr` のモック化困難性は impl-notes
  に注記済み（reject 対象としない）
- 1.8（公開 IP に解決される URL は従来通り接続） —
  `TestClassifyIP/ipv4_public_8_8_8_8`、`ipv4_public_1_1_1_1`、`ipv6_public_google`、
  `ipv4_mapped_public`、`TestCheckHostIPLiteral_AllowsPublicIPLiterals`、
  `TestCheckHostIPLiteral_AllowsHostnames`（3 ケース）、`TestFetchAllowsRoundTripFuncOverride`

### Requirement 2（DNS Rebinding TOCTOU 耐性）

- 2.1（TCP 接続直前の IP 再検査） — `TestGuardedDialContext_RejectsBlockedIPLiteralAddress`（5 ケース）。
  `internal/fetcher/ssrf.go:220-261` の `guardedDialContext` クロージャが
  `http.Transport.DialContext` に組み込まれ、address parse → `classifyIP` → base.DialContext 委譲の順
- 2.2（DNS rebinding rebind 想定） — `TestGuardedDialContext_RebindingResolverReturnsPrivateIP`。
  http.Transport が IP 解決後に IP リテラル address で dial する挙動を再現し、guard が address に
  含まれる private IP を再検査して拒否することを検証
- 2.3（リダイレクト追従時の再検査） — `internal/fetcher/fetcher.go:60-72` の `CheckRedirect` で
  `checkHostIPLiteral(req.URL.String())` を呼び出し、IP リテラルへのリダイレクトを拒否。
  ホスト名リダイレクトは DialContext 層で再検査される構造
- 2.4（DialContext 経路で都度検査） — `guardedDialContext` 実装と
  `TestGuardedDialContext_RejectsBlockedIPLiteralAddress` / `TestGuardedDialContext_RebindingResolverReturnsPrivateIP`
  で都度実行される設計を検証

### Requirement 3（拒否時のエラー識別と監視ログ）

- 3.1（識別可能 sentinel） — `var ErrBlockedIP = errors.New("blocked_ip")` (ssrf.go:16)、
  `*BlockedIPError.Unwrap() = ErrBlockedIP` (ssrf.go:52)。既存
  `ErrTooLarge` / `ErrTooManyRedir` / `ErrBadStatus` / `ErrNoContent` とは別 sentinel。
  `TestCheckHostIPLiteral_BlocksIPLiteralURLs` 内で `errors.Is(err, ErrBlockedIP)` と
  `errors.As(err, &be)` の両方を assert
- 3.2（Worker 理由コード追加） — `cmd/worker/main.go:140-142` で `errors.Is(err, fetcher.ErrBlockedIP)` →
  `"blocked_ip"`。`TestClassifyFetchErrorBlockedIP/sentinel_error`、`/wrapped_blocked_ip_error`（
  Unwrap チェーン経由でも分類されることを別の偽 wrapper 型で検証）
- 3.3（slog に item ID と拒否カテゴリを 1 行残す） — `cmd/worker/main.go:150-162` の
  `logFetchFailure` で `errors.As(err, &blockedErr)` 経由で `blocked_category` フィールドを付加。
  slog 出力内容を直接 assert するテストは未追加だが、コードは単純な分岐で目視確認可能（impl-notes に
  注記あり、reject 対象としない）
- 3.4（機密情報非漏洩） — `TestBlockedIPError_DoesNotLeakURLPath`（path/query/Cookie が
  error 文字列に含まれないことを assert）、`TestCheckHostIPLiteral_BlocksIPLiteralURLs` 内の
  `if strings.Contains(be.Error(), "/admin") || strings.Contains(be.Error(), "meta-data")` assert。
  `logFetchFailure` も raw URL を一切ログに出力しない（`item_id` / `reason` / `blocked_category` のみ）

### Requirement 4（テストおよびビルトイン IP 検査の検証可能性）

- 4.1（IP 範囲判定の独立テスト可能ユニット） — `classifyIP` / `classifyIPv4` / `checkHostIPLiteral`
  / `guardedDialContext` の 4 関数として独立。`TestClassifyIP` で 24 ケース table-driven
- 4.2（既存 roundTripFunc テスト互換） — 既存 7 件（`TestFetchSuccess` ほか）が cached green、
  `TestFetchAllowsRoundTripFuncOverride` で Client 全置換パターンの非破壊を明示検証
- 4.3（SSRF 拒否動作の最低 1 ケース） — loopback / private / link-local / metadata / rebinding
  各 1 ケース以上を `TestGuardedDialContext_RejectsBlockedIPLiteralAddress`（5 ケース）と
  `TestGuardedDialContext_RebindingResolverReturnsPrivateIP` で担保
- 4.4（公開 IP 接続の正常系維持） — `TestFetchAllowsRoundTripFuncOverride`、既存
  `TestFetchSuccess` ほか

### Non-Functional Requirements

- NFR 1.1（既定有効・opt-in 不要） — `Fetcher.New()` の戻り値で常に `guardedDialContext` 組込み
  Transport を使用、設定オプション無し（fetcher.go:39-75）
- NFR 1.2（deny-by-default） — `classifyIP(nil)` が `categoryUnparseableIP` を返し guard が
  reject。`TestClassifyIP_NilReturnsUnparseable`
- NFR 1.3（ログ機密情報禁止） — `logFetchFailure` は URL を出力せず、`BlockedIPError.Error()` も
  path/query/Cookie を含まない（`TestBlockedIPError_DoesNotLeakURLPath`）
- NFR 2.1（< 5ms オーバーヘッド） — 公開 IP パスでは `classifyIP` が定数時間で抜け、
  `base.DialContext` に委譲のみ（実測ベンチは未追加だが設計上担保、reject 対象としない）
- NFR 2.2（拒否を別カテゴリで集計） — `"blocked_ip"` 理由コードでユニーク、
  `blocked_category` フィールド付加で更に細分集計可能
- NFR 3.1（既存 fetch 挙動互換） — Transport の他項目は `http.DefaultTransport` 相当に揃え、
  タイムアウト・リダイレクト上限・サイズ制限変更なし。既存 7 件テスト緑
- NFR 3.2（既存テスト破壊なし） — `go test ./...` 全パッケージ green（reviewer 再実行で確認）

## Findings

なし

## Summary

Issue #76 の SSRF 防御要件（Req 1〜4 + NFR 1〜3）について、`internal/fetcher/ssrf.go` に
`classifyIP` / `checkHostIPLiteral` / `guardedDialContext` を新設し、`Fetch()` 入口の早期検査と
`http.Transport.DialContext` フックの 2 段階で IP を判定する設計が AC を網羅的にカバー。
`go test ./...` 全パッケージ green、boundary も `internal/fetcher` と `cmd/worker` に閉じている
（Out of Scope のプロキシ・許可リスト・UI 切替には踏み込んでいない）。
DNS モック / slog 出力 assert / ベンチマークは未追加だが、いずれも impl-notes で根拠が示されており
3 カテゴリ（AC 未カバー / missing test / boundary 逸脱）には該当しない。

RESULT: approve
