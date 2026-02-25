# Gap Analysis: extension-article-reader

## Analysis Scope
- Feature: `extension-article-reader`
- Analysis Date: 2026-02-24
- Spec Phase: `requirements-generated`（requirements 未承認）
- Method: `.kiro/settings/rules/gap-analysis.md` に準拠

## Current State Investigation

### Relevant Existing Assets
- Extension UI/logic
  - `extension/sidepanel.html`
  - `extension/sidepanel.css`
  - `extension/sidepanel.js`
  - `extension/manifest.json`
  - `extension/background.js`
- API/Auth
  - `internal/server/server.go` (`/v1/auth/extension/exchange`, `/v1/items`, `/v1/items/{id}/capture`, `/v1/tags`)
- Persistence/Search
  - `internal/store/store.go` (`ListItems`, `SuggestTags`, `CreateItem`, `SeedCapturedContent`)
  - `migrations/001_init.sql` (pg_trgm indexes, `item_contents.content_search`, `tags.normalized_name`)

### Architecture & Convention Fit
- レイヤー分離（handler薄く、DBは`internal/store`集中）は steering と整合。
- Extension は Vanilla JS + API 呼び出しで既存方針（依存最小）と整合。
- 検索は既存の `ListItems` を再利用でき、追加の検索専用API新設は現時点で不要。

## Requirement-to-Asset Map

| Requirement | Existing Assets | Coverage | Gaps / Notes |
|---|---|---|---|
| 1. 認証状態に応じた初期画面 | `sidepanel.html` の `loginScreen`/`readerScreen`、`sidepanel.js` の `showLoginScreen()` 初期化フロー | Mostly Covered | `token`未保持時は認証API未呼び出し。requirements内容を満たす。 |
| 2. 固定ユーティリティ導線 | `utility-bar`、`openWebUI()`、`logout()` | Covered | `Go to website` は `/ui/items` を新規タブで開く。ログアウトで token と Google auth cache をクリア。 |
| 3. 保存セクション + 非同期取得 | `save-panel`、`saveCurrentTab()`、`extractPageCapture()`、`sendCapturedContent()`、`POST /v1/items`、`POST /v1/items/{id}/capture` | Covered with Constraints | Constraint: `chrome.scripting` 実行不可ページでは本文抽出できず capture 送信がスキップされる。 |
| 4. 区切り線下の検索と一覧 | `section-divider`、`search-panel`、`renderItems()`、`GET /v1/items` + `Store.ListItems` | Mostly Covered | 一覧取得は `per_page=50` 固定。大量件数時の表示戦略（ページング/無限スクロール）は未定義。 |
| 5. 責務境界と障害時挙動 | Sidepanelに edit/delete/refetch 導線なし、`isAuthFailureStatus` で未認証遷移、権限チェック `ensureAPIAccessPermission` | Covered with Minor Gap | 拡張機能向け自動テストが未整備。回帰耐性が不足。 |

### Gap Tags
- Missing
  - Side Panel 実装の自動テスト（UI状態遷移・APIエラー・権限拒否・検索表示）
- Constraint
  - APIアクセスには host permission/CORS 設定整備が前提
  - 本文抽出は Chrome の実行可能タブ制約に依存
  - 検索結果表示は 50 件固定
- Unknown (Research Needed)
  - データ増加時の `q` 検索（title/excerpt/content_search/tags）応答性能の実測閾値
  - tag 部分一致検索の UX（曖昧一致の期待値）と DB 負荷のバランス

## Implementation Approach Options

### Option A: Existing Sidepanel を拡張継続
- 変更先
  - `extension/sidepanel.js` に機能を追加
- Pros
  - 既存コードを最短で活用できる
  - 追加ファイルが少なく導入が速い
- Cons
  - 1ファイル肥大化（すでに約700行）
  - 認証/保存/検索/UI描画の関心が密結合
- Fit
  - 直近リリース優先時に有効

### Option B: Sidepanel 専用の新規分割コンポーネント化
- 新設候補
  - `sidepanel-auth.js`, `sidepanel-api.js`, `sidepanel-items.js`, `sidepanel-save.js`
- Pros
  - 責務分離が明確でテストしやすい
  - 将来機能追加（並び替え、軽量フィルタ）時の保守性が高い
- Cons
  - 初期分割コストがある
  - モジュール読込順・依存設計が必要
- Fit
  - 中長期運用を重視する場合に有効

### Option C: Hybrid（推奨）
- 方針
  - まず既存 `sidepanel.js` を維持しつつ、再利用価値が高い関数群（auth/api error handling/tag helper）を段階抽出
  - 同時に `sidepanel` の最小テストセットを先行整備
- Pros
  - 速度と保守性のバランスが良い
  - 大規模リライトを避けながら技術負債を抑制できる
- Cons
  - 移行期間中は旧/新ロジックの混在管理が必要

## Complexity & Risk
- Effort: **M (3-7 days)**
  - 理由: 主要機能は実装済みで、残る中心は品質補強（テスト、境界条件、表示戦略整理）
- Risk: **Medium**
  - 理由: 認証/CORS/権限の環境依存点と、検索件数増加時のUX・性能が未検証

## Design Phase Recommendations
- Preferred Direction
  - Option C（Hybrid）を基準に設計書を作成
- Key Decisions to Lock in Design
  - Sidepanel の表示件数戦略（50件固定継続 or pagination/infinite scroll）
  - `sidepanel` テスト方針（DOM + fetch mock の新規整備）
  - 認証失効/権限不足/通信失敗のメッセージ仕様の統一

## Research Needed (Carry Forward)
1. 大量データ（例: 1万件）時の `/v1/items?q=...` レイテンシ実測と許容値
2. `tag` 部分一致検索の期待UX（完全一致優先 or 部分一致優先）
3. `per_page=50` 超の閲覧行動に対するUI設計（ページング/遅延読込）
4. 拡張機能の host permission 設定（本番APIドメイン固定時の最小権限化）

## Conclusion
- 本機能は要件の主要部分を既存実装でほぼ充足しており、実装ギャップは「機能不足」より「品質と拡張性の明文化」に寄っている。
- 設計フェーズでは、テスト戦略・件数戦略・運用設定（CORS/permissions）を優先的に固定するのが妥当。
