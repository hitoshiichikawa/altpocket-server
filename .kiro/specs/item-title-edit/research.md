# Research & Design Decisions

## Summary
- **Feature**: `item-title-edit`
- **Discovery Scope**: Extension（既存タグ編集UIにタイトル編集を統合、新規PATCHエンドポイント追加）
- **Key Findings**:
  - `PATCH /v1/items/{id}` を新設し、既存 `PUT /v1/items/{id}/tags` は互換用に維持する方針が最適
  - 既存タグ編集の openEditor/closeEditor にタイトル input の切替を統合できる
  - Store 層は新規メソッド `PatchItem` を追加し、既存 `ReplaceItemTags` は変更しない

## Research Log

### 既存タグ編集パターンの分析
- **Context**: タイトル編集をタグ編集に統合可能か調査
- **Sources Consulted**: `static/app.js` (L121-365), `templates/item_detail.html`, `internal/server/server.go` (L522-553)
- **Findings**:
  - タグ編集は `openEditor()` / `closeEditor()` でモード切替を管理
  - `originalTags` で元の値を保持しキャンセル時に復元
  - 保存中は `detailTagSaveBtn.disabled = true` で二重送信防止
  - openEditor にタイトル input の表示切替を追加し、closeEditor でタイトルの復帰も行えば統合可能
- **Implications**: 既存の編集フローを拡張する形で統合可能。新規ボタン不要

### APIエンドポイント設計
- **Context**: タイトル+タグの一括更新エンドポイントの方式を決定
- **Sources Consulted**: `internal/server/server.go` (L127-135)
- **Findings**:
  - 選択肢1: 既存 `PUT /v1/items/{id}/tags` を拡張 → 名前と実態が乖離
  - 選択肢2: `PATCH /v1/items/{id}` を新設 → セマンティクス明確、将来の拡張性あり
  - 選択肢3: リネーム + エイリアス → コード管理コスト増
  - 選択肢2 を採用。既存エンドポイントは拡張機能互換のためそのまま維持
- **Implications**: 新規ルート1行・新規ハンドラー1関数追加。既存コード変更なし

### Store層の設計方針
- **Context**: 新規 `PatchItem` メソッドと既存 `ReplaceItemTags` の関係
- **Sources Consulted**: `internal/store/store.go` (L461-540)
- **Findings**:
  - `ReplaceItemTags` は既にトランザクション内で所有権チェック → タグ操作を実行
  - 新規 `PatchItem` はトランザクション内でタイトル更新 + タグ置換を一括実行
  - `PatchItem` 内部から既存のタグ置換ロジックを呼ぶか、インラインで実装するか
  - `ReplaceItemTags` のロジックを `PatchItem` に直接実装し、`ReplaceItemTags` から `PatchItem` を呼ぶ形にリファクタリングするのがシンプル
- **Implications**: `ReplaceItemTags` は `PatchItem(ctx, userID, itemID, tagNames, nil)` の薄いラッパーに変更可能

## Design Decisions

### Decision: PATCH エンドポイント新設 + 既存エンドポイント維持
- **Context**: タイトル+タグ更新のAPIエンドポイント方式を決定
- **Alternatives Considered**:
  1. `PUT /v1/items/{id}/tags` を拡張 — 変更最小だが名前と実態が乖離
  2. `PATCH /v1/items/{id}` を新設 — セマンティクス明確、既存は互換維持
  3. リネーム + エイリアス — 名前は正しいがコード管理コスト増
- **Selected Approach**: Option 2（`PATCH /v1/items/{id}` 新設）
- **Rationale**: PATCH はリソースの部分更新というHTTPセマンティクスに合致。エンドポイント名から機能を類推可能。将来 excerpt 等の追加にも自然に対応できる
- **Trade-offs**: ハンドラーが1つ増えるが、既存 `handleUpdateItemTags` のコードは変更不要
- **Follow-up**: `ReplaceItemTags` を `PatchItem` のラッパーにリファクタリング

### Decision: 統合編集アプローチ
- **Context**: タイトル編集のUI方式を決定
- **Alternatives Considered**:
  1. 分離方式 — タイトル専用の編集ボタンを追加
  2. 統合方式 — 既存タグ編集に統合し、1ボタンで両方を操作
- **Selected Approach**: 統合方式（Option 2）
- **Rationale**: ユーザー操作がシンプル。タイトルとタグは同じコンテキスト（アイテムメタデータ編集）に属する
- **Trade-offs**: タグ編集のロジックが若干複雑になるが、保存操作のアトミック性が確保される

## Risks & Mitigations
- リスク1: タイトル空文字保存 → クライアント・サーバー両方でバリデーション実施
- リスク2: 既存 `PUT /tags` との整合性 → 既存エンドポイントは変更せず、`ReplaceItemTags` をラッパー化
- リスク3: XSS（ユーザー入力タイトル） → Go テンプレートの自動エスケープ + JS 側で textContent 使用

## References
- 既存コード: `internal/server/server.go`, `internal/store/store.go`, `static/app.js`, `templates/item_detail.html`
