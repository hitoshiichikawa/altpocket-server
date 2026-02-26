# ギャップ分析: extension-content-prefetch

## 1. 現状調査

### 既存アセットマップ

| 要件領域 | 関連既存コード | 状態 |
|---------|-------------|------|
| ページメタデータ抽出 | `extension/sidepanel.js` → `extractPageCapture()` | ✅ 既存（title + content_full を抽出済み） |
| 登録APIリクエスト | `extension/sidepanel.js` → `saveCurrentTab()` → `apiClient.createItem()` | ⚠️ 拡張必要（`{ url, tags }` のみ送信） |
| API受け入れ | `internal/server/server.go` → `handleCreateItem()` | ⚠️ 拡張必要（`{ url, tags }` のみ受理） |
| DB保存 | `internal/store/store.go` → `CreateItem()` | ⚠️ 拡張必要（title/excerpt を INSERT に含まない） |
| Worker上書き | `internal/store/store.go` → `UpdateFetchSuccess()` | ✅ 変更不要（成功時は無条件上書き） |
| Worker失敗時保持 | `internal/store/store.go` → `UpdateFetchFailure()` | ✅ 変更不要（title/excerpt に触れない） |
| DBスキーマ | `migrations/001_init.sql` → `items.title`, `items.excerpt` | ✅ カラム既存（DEFAULT ''） |
| 拡張機能テスト | `extension/sidepanel.test.mjs` | ⚠️ 拡張必要 |
| 契約テスト | `internal/server/extension_contract_test.go` | ⚠️ 拡張必要 |

### 重要な既存パターン

- **抽出ロジック**: `extractPageCapture()` は `chrome.scripting.executeScript` でアクティブタブから `document.title` と本文テキストを取得。除外セレクタ、正規化、文字数制限を適用済み
- **キャプチャ分離**: 現在は保存（`POST /v1/items`）とキャプチャ（`POST /v1/items/{id}/capture`）を2段階で実行。キャプチャは fire-and-forget
- **SeedCapturedContent vs UpdateFetchSuccess**: Seed は `fetch_status <> 'success'` の場合のみ更新（worker完了後の上書き防止）。UpdateFetchSuccess は無条件上書き

## 2. 要件実現性分析

### 技術的ニーズ

| 要件 | 必要な変更 | 複雑度 |
|-----|----------|-------|
| R1: ページメタデータ事前取得 | `extractPageCapture` を保存前に呼び出す軽量版を追加 or 既存関数を再利用 | 低 |
| R2: APIメタデータ受け入れ | `handleCreateItem` のリクエスト構造体に `Title`/`Excerpt` 追加、`CreateItem` のシグネチャ・SQL拡張 | 低 |
| R3: Worker共存 | 変更不要（既存の `UpdateFetchSuccess`/`UpdateFetchFailure` が要件を満たす） | なし |
| R4: 抽出制約 | `extractPageCapture` の既存ロジックがほぼそのまま再利用可能 | 低 |

### ギャップと制約

- **Missing**: `handleCreateItem` に title/excerpt を受け取る仕組み
- **Missing**: `store.CreateItem` の INSERT に title/excerpt を含める
- **Missing**: `saveCurrentTab()` で保存前にメタデータ抽出を行う処理
- **Constraint**: 既存キャプチャフロー（保存後の `sendCapturedContent`）は full content 用として残す必要がある。プレフィルは excerpt（200文字）のみ

## 3. 実装アプローチ

### Option A: 既存コンポーネント拡張（推奨）

既存の `createItem` フローを拡張し、保存リクエストに title/excerpt を含める。

**変更対象ファイル**:
1. `extension/sidepanel.js` — `saveCurrentTab()` で保存前にメタデータ抽出、`createItem` ペイロードに追加
2. `internal/server/server.go` — `handleCreateItem` リクエスト構造体・`createItem` 関数シグネチャ拡張
3. `internal/store/store.go` — `CreateItem` シグネチャ拡張、INSERT SQL に `title`, `excerpt` 追加

**トレードオフ**:
- ✅ 最小限の変更で実現可能（3ファイル）
- ✅ 既存パターンに沿った拡張
- ✅ 後方互換性維持（title/excerpt は空文字でもOK）
- ✅ Worker側の変更不要
- ❌ `createItem` の引数が増えるが、許容範囲

### Option B: 新しいプレフィルエンドポイント作成

`POST /v1/items/{id}/prefill` のような新エンドポイントを追加し、作成直後にプレフィルデータを送信。

**トレードオフ**:
- ✅ 既存 `createItem` API を変更しない
- ❌ 2リクエスト必要（本質的に現在のキャプチャフローと同じ問題）
- ❌ レース条件リスク（worker が先に動く可能性）
- ❌ 不要な複雑性

### Option C: 既存キャプチャフローの流用

`extractPageCapture` の呼び出しを保存前に移動し、save + capture を同期的に実行。

**トレードオフ**:
- ✅ 新しい関数を作らない
- ❌ `extractPageCapture` は full content 用（CONTENT_CAPTURE_LIMIT が大きい）で、excerpt 200文字とは別目的
- ❌ 保存前に大量テキスト抽出を待つため UX が遅延する可能性
- ❌ 2つのAPI呼び出しが必要（createItem → capture）のまま

## 4. 複雑度・リスク評価

- **Effort**: **S**（1〜3日）— 3ファイルの既存パターン拡張、スキーマ変更なし
- **Risk**: **Low** — 後方互換、Worker側変更不要、既存テストカバレッジあり

## 5. 設計フェーズへの推奨事項

### 推奨アプローチ
**Option A**（既存コンポーネント拡張）を推奨。最小限の変更で要件を満たし、アーキテクチャの一貫性を保てる。

### 重要な設計判断ポイント
1. **抽出関数の再利用 vs 新規作成**: `extractPageCapture` を200文字制限で呼ぶか、軽量版の新関数を作るか
2. **抽出タイミング**: 保存ボタン押下時に同期的に抽出 → ペイロードに含める
3. **キャプチャフローとの関係**: 既存の fire-and-forget キャプチャ（full content 用）はそのまま残す

### Research Needed
- なし（すべて既存技術・パターンの範囲内で実現可能）
