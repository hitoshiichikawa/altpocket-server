# Research & Design Decisions

## Summary
- **Feature**: `extension-article-reader`
- **Discovery Scope**: Extension（既存システム拡張、light discovery 実施）
- **Key Findings**:
  - Side Panel + OAuth 連携の主要フローは現行実装に存在し、設計の主眼は新規機能追加より責務境界の明確化にある。
  - 検索は既存 `/v1/items`（`q`, `sort`, `page`, `per_page`）で成立しており、PostgreSQL `pg_trgm` ベースの relevance 順序を活用できる。
  - 実運用上の主要リスクは OAuth/CORS/権限の環境依存であり、設計ではフォールト時の画面遷移と通知の一貫性を先に固定する必要がある。

## Research Log

### Side Panel 統合方式
- **Context**: popup から sidepanel へ移行済みのため、設計では既存 UI 資産を前提に境界のみ再整理する必要がある。
- **Sources Consulted**:
  - `extension/manifest.json`
  - `extension/sidepanel.html`
  - `extension/sidepanel.js`
  - https://developer.chrome.com/docs/extensions/reference/api/sidePanel
- **Findings**:
  - `side_panel.default_path` と `permissions: ["sidePanel"]` が定義され、MV3 Side Panel として成立している。
  - 認証前後画面は単一 HTML 内の表示切替で実現されており、スクリーン遷移は state 管理の責務として独立させやすい。
- **Implications**:
  - 設計では「画面状態管理」と「API 呼び出し」を別責務に固定し、将来の分割実装（view/service 層）に備える。

### OAuth と拡張機能認証フロー
- **Context**: `Sign in with Google` から `id_token` を取得し、サーバー側の extension exchange で JWT を受け取る流れが要件の中核。
- **Sources Consulted**:
  - `extension/sidepanel.js`
  - `internal/server/server.go`
  - https://developer.chrome.com/docs/extensions/reference/api/identity
- **Findings**:
  - `chrome.identity.getRedirectURL()` と `launchWebAuthFlow()` により `id_token` を取得する構成。
  - API 側は `/v1/auth/extension/exchange` で `id_token` を検証し、登録済みユーザーに JWT を発行する。
  - 401/403 は extension 側でセッション失効として扱い、ログアウト + 未認証画面遷移へ統一できる。
- **Implications**:
  - 設計上、auth failure を cross-cutting concern として API client 層で一元処理する。

### 動的権限と API 到達性
- **Context**: Side Panel は任意 API オリジンへのアクセスが必要で、環境差（本番/開発）で失敗が発生しやすい。
- **Sources Consulted**:
  - `extension/manifest.json`
  - `extension/sidepanel.js`
  - `internal/server/server.go`
  - https://developer.chrome.com/docs/extensions/reference/api/permissions
  - https://developer.chrome.com/docs/extensions/develop/concepts/declare-permissions
- **Findings**:
  - `optional_host_permissions: ["https://*/*"]` + `chrome.permissions.contains/request` を利用して実行時許可を要求している。
  - API 側は `CORS_ALLOW_ORIGINS` による allowlist と同一 host 許可ロジックを持つ。
- **Implications**:
  - 設計で「権限不足」「CORS 不一致」「認証失効」を別カテゴリで扱い、UI 通知と再試行導線を分離する。

### 検索品質と relevance 順序
- **Context**: 要件では件名/本文/タグの部分一致検索と relevance 並びを求めている。
- **Sources Consulted**:
  - `internal/store/store.go`
  - `migrations/001_init.sql`
  - https://www.postgresql.org/docs/current/static/pgtrgm.html
- **Findings**:
  - `ListItems` は `title`, `excerpt`, `content_search`, `canonical_url`, `normalized_name` を `ILIKE` + `similarity` で評価する。
  - `sort=relevance` かつ `q != ''` の場合に `score DESC, created_at DESC` が適用される。
  - `per_page` は 10/20/30/40/50 の許可制で、extension は 50 固定で呼び出している。
- **Implications**:
  - 設計は既存検索 API を再利用し、UI 側の一覧表示責務に集中する。
  - 大量データ時の応答性能は実測タスクとして残す。

### テスト資産と品質ギャップ
- **Context**: 要件 5 の障害時挙動を設計に落とすため、既存自動テストの範囲を確認。
- **Sources Consulted**:
  - `extension/sidepanel.test.mjs`
  - `node --test extension/sidepanel.test.mjs` 実行結果
  - `go test ./...` 実行結果
- **Findings**:
  - extension テストは 10 ケースで pass。
  - 認証、保存、検索、401復帰、権限不足、保存 API ネットワーク失敗を網羅。
  - 一覧 API ネットワーク失敗、`Go to website` 導線の自動テストは未追加。
- **Implications**:
  - 設計の testing strategy では「残る失敗系ケース」を明示的に追加対象として扱う。

## Architecture Pattern Evaluation

| Option | Description | Strengths | Risks / Limitations | Notes |
|--------|-------------|-----------|---------------------|-------|
| Option A | `sidepanel.js` 単体拡張を継続 | 変更が速い、差分が小さい | 状態/API/UI の再肥大化 | 小規模変更のみ向く |
| Option B | 完全分割（auth/api/view/state を別ファイル） | テスト容易性・保守性が高い | 初期分割コスト、配線再構成 | 一括移行はリスクが高い |
| Option C | Hybrid（段階分離） | 既存挙動を維持しつつ改善可能 | 移行期間の混在管理が必要 | **採用**（Phase 1 実装済み） |

## Design Decisions

### Decision: Hybrid 境界（screenState + apiClient + appState）を正式化
- **Context**: 機能は成立しているが、責務境界が曖昧だと変更時に回帰しやすい。
- **Alternatives Considered**:
  1. 単一ファイル継続（Option A）
  2. 全面モジュール分割（Option B）
  3. 段階分離（Option C）
- **Selected Approach**: Option C を採用し、画面状態・API 呼び出し・共有状態を分離して契約を明示する。
- **Rationale**: 現行挙動を保ったまま設計の可読性と変更安全性を上げられる。
- **Trade-offs**: 移行期間に設計文書と実装の同期管理が必要。
- **Follow-up**: 描画責務の追加分離（items/tags presenter）を次段で検討。

### Decision: 認証失効（401/403）を API client 層で一元処理
- **Context**: endpoint ごとの分岐重複は不整合を生みやすい。
- **Alternatives Considered**:
  1. 各ユースケースで個別処理
  2. グローバルエラーハンドラ化
- **Selected Approach**: `onAuthFailure` コールバックで logout + login 画面遷移を集約。
- **Rationale**: 振る舞い一貫性を確保し、要件 5.3 の充足を保証しやすい。
- **Trade-offs**: 認証失敗と他エラーの区別が UI 文言上はまだ粗い。
- **Follow-up**: エラー分類ごとの通知文言を設計で固定。

### Decision: 検索は既存 `/v1/items` 契約を維持
- **Context**: 新規検索 API を増やすと backend/frontend の同期コストが増える。
- **Alternatives Considered**:
  1. 専用検索 API 新設
  2. 既存 `/v1/items` 拡張利用
- **Selected Approach**: `q + sort=relevance` と `per_page=50` を維持。
- **Rationale**: 既存インデックス・スコア計算を再利用でき、導入コストが低い。
- **Trade-offs**: 大量件数での UX（段階読み込み）は別タスクとして残る。
- **Follow-up**: レイテンシ計測と pagination 戦略を設計後続タスクで定義。

### Decision: 保存後 capture は fire-and-forget を維持
- **Context**: 保存の応答性を優先しつつ、本文検索性を確保する必要がある。
- **Alternatives Considered**:
  1. 保存完了まで capture 同期
  2. 保存完了後に非同期 capture
- **Selected Approach**: `created=true` 時のみ capture API を非同期送信。
- **Rationale**: 要件 3 の UX と既存プロダクト方針（保存と取得の分離）に一致。
- **Trade-offs**: capture 失敗時の回復は後続処理依存。
- **Follow-up**: capture 失敗の観測性（メトリクス/ログ）強化。

## Risks & Mitigations
- OAuth クライアント設定不整合（client_id / redirect） — 事前検証チェックリストと環境別設定テンプレートを整備。
- CORS allowlist ミス（extension id 変更時） — `CORS_ALLOW_ORIGINS` 更新運用を runbook 化。
- 権限拒否での操作中断増加 — 権限要求タイミングを user gesture に限定し、失敗時に再試行手順を表示。
- `relevance` 応答遅延（データ増加時） — ベンチマーク閾値設定と段階読み込み導入可否を評価。
- 失敗文言の粒度不足（`Login error`） — エラー種別マッピングを設計で先に固定。

## References
- [chrome.sidePanel API](https://developer.chrome.com/docs/extensions/reference/api/sidePanel) — Side Panel の可用性、権限、挙動。
- [chrome.identity API](https://developer.chrome.com/docs/extensions/reference/api/identity) — `getRedirectURL` / `launchWebAuthFlow` 契約。
- [chrome.permissions API](https://developer.chrome.com/docs/extensions/reference/api/permissions) — 実行時権限の `contains/request`。
- [Declare permissions (Chrome Extensions)](https://developer.chrome.com/docs/extensions/develop/concepts/declare-permissions) — `optional_host_permissions` の運用指針。
- [PostgreSQL pg_trgm (current)](https://www.postgresql.org/docs/current/static/pgtrgm.html) — `similarity` 関数と trigram index の性質。
- [sidepanel.js](/Users/hitoshi/Documents/GitHub/altpocket-server/extension/sidepanel.js) — 現行 extension 実装。
- [store.go](/Users/hitoshi/Documents/GitHub/altpocket-server/internal/store/store.go) — relevance 検索ロジック。
