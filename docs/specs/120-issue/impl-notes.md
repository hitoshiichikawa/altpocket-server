# Implementation Notes — Issue #120 カードからタグへのドラッグ&ドロップでタグ付与

## 採用した実装判断 (Open Questions)

- **(b) 永続化エンドポイント**: `POST /v1/items/bulk-tag` を **単一 item_id** で利用。
  bulk-tag は additive かつ冪等で、server 側で所有権チェック・タグ正規化・構造化ログを
  既に行う。既存タグ集合を読まずに Req 2.3/2.4（重複なし・再ドロップ成功）と
  Req 5.3/5.4（所有限定・セッション失効時非実行）を自然に満たせるため採用
  （全置換 PUT は union 計算が必要で冪等性の取り扱いが複雑）。新規 API は追加していない。
- **(a) タッチ代替手段**: カードの `[data-card-tag-add]`「タグ付与」ボタン（SSR で hidden）を、
  JS が `matchMedia('(pointer: coarse)')` でタッチ環境を検出したときだけ表示する。tap で
  tagging モードに入り、続けてサイドバー/ボトムシートのタグを tap すると **ドロップと同一の
  `assignTag` 関数**で付与する。ロジック共有で重複と乖離を最小化（Req 4.2 の挙動同一性を保証）。
- **(c) 視覚フィードバック**: 最小限の class 付与 — `.is-dragging`（カード半透明/Req 3.1）、
  `.is-drop-target`（タグ要素に outline + 背景/Req 3.2）、`.is-tag-target`（tagging モード対象
  カードに outline/Req 4.1）。色のみに依存せず outline/opacity で区別（NFR 4.2）。

## 変更ファイル

- `templates/items_list.html` — カードに `draggable="true"` / `data-item-card`、actions に
  `button[data-card-tag-add]`（初期 hidden）を追加。
- `templates/items.html` — サイドバー/ボトムシートの `.tag-filter-option` に
  `data-tag-drop-target` / `data-tag-name` / `data-tag-normalized` を付与。`items_drag_tag.js`
  を defer で読み込み。
- `static/items_drag_tag.js` — DnD + タッチ代替の実装（IIFE + `init()` + `_debug`、document
  delegated、auto-init skip flag）。chip 再構築は `items_bulk_actions.js` の SSR contract と一致。
- `static/items_drag_tag.test.mjs` — 単体テスト 14 件（fake DOM + fetch stub）。
- `static/style.css` — DnD/tagging の視覚状態 CSS。
- `internal/ui/render_test.go` — SSR 契約を担保する `TestDragTagSSRContract`。

## AC ↔ テスト対応表

| AC | 担保テスト |
|---|---|
| 1.1 カードが draggable | `TestDragTagSSRContract`「Req 1.1」(render_test.go) |
| 1.2 タグ要素がドロップ先 | `TestDragTagSSRContract`「Req 1.2 / 3.4」 |
| 1.3 ドロップで付与 | drag-tag.test「Req 1.3 / 1.4 / 2.1」 |
| 1.4 カード tag 表示反映 | drag-tag.test「Req 1.3 / 1.4 / 2.1」(chip 再描画 assert) |
| 1.5 対象外ドロップで no-op | drag-tag.test「Req 1.5」 |
| 1.6 ドラッグ中に絞り込み等不変 | drag-tag.test「NFR 2.2 / 2.3」+ 設計上 URL/filter を一切触らない |
| 2.1 既存 EP で永続化 | drag-tag.test「Req 1.3」(bulk-tag URL assert) |
| 2.2 リロード後保持 | bulk-tag が DB 永続化（server `handleBulkTagItems` / store, 既存テスト済）+ chip 再描画 |
| 2.3 冪等で重複なし | drag-tag.test「Req 2.3 / 2.4」(chip 1 件 assert) |
| 2.4 既保持タグ再ドロップ成功 | drag-tag.test「Req 2.3 / 2.4」(error 0 件 assert) |
| 2.5 新規 API 追加なし | bulk-tag 既存 EP のみ使用（コード上 fetch 先 1 種）+ render_test の script 読込 |
| 2.6 同一タグ正規化 | server `tag.Normalize`（既存）+ SSR の正規化済み `data-tag-normalized` を送信 |
| 3.1 ドラッグ中視覚状態 | drag-tag.test「Req 3.1」 |
| 3.2 ドロップ先候補視覚状態 | drag-tag.test「Req 3.2 / 3.3」 |
| 3.3 中断で視覚状態解除 | drag-tag.test「Req 3.2 / 3.3」(dragleave/dragend assert) |
| 3.4 デスクトップサイドバーをドロップ先 | `TestDragTagSSRContract`「Req 1.2 / 3.4」 |
| 3.5 サイドバー/ボトムシート同一挙動 | drag-tag.test「Req 3.4 / 3.5」+ render_test（≥2 ドロップ先 / 空白含む名）|
| 4.1 タッチ代替手段提供 | drag-tag.test「Req 4.1」(coarse 表示 / 非 coarse 非表示) + render_test |
| 4.2 代替が付与/永続化/冪等で同一 | drag-tag.test「Req 4.2 / 4.3」(同一 assignTag → bulk-tag) |
| 4.3 代替も既存 EP 利用 | drag-tag.test「Req 4.2 / 4.3」(bulk-tag URL assert) |
| 5.1 失敗時にカード表示を成功化しない | drag-tag.test「Req 5.1 / 5.2」「Req 5.5」(chip 不付与 assert) |
| 5.2 失敗を通知 | drag-tag.test「Req 5.1 / 5.2」(toast.error assert) |
| 5.3 セッション失効時は変更しない | server 401 → failed/非 200 → 非反映+通知（drag-tag.test「Req 5.5」が failed 経路担保）|
| 5.4 所有アイテム限定 | server bulk-tag が所有権で collapse（既存）→ failed → 非反映（drag-tag.test「Req 5.5」）|
| 5.5 非所有 id で変更せず通知 | drag-tag.test「Req 5.5」 |
| NFR 1.1/1.2 既存 EP のみ/契約不変 | fetch 先 bulk-tag 1 種・request/response 形は既存契約のまま（変更なし）|
| NFR 2.1 プログレッシブエンハンスメント | JS 無効時は本ファイル未評価・既存編集経路維持（型 `type=button`/SSR fallback）|
| NFR 2.2/2.3/2.4 #117/#118/#115 非回帰 | drag-tag.test「NFR 2.2 / 2.3」+ 通常 click 非 intercept・全既存テスト 177 pass |
| NFR 3.1/3.2 レスポンス品質 | 同期 DOM（is-dragging/is-drop-target）+ 成功時 chip 即時再描画（fetch 完了後）|
| NFR 4.1/4.2 a11y | タッチ代替/既存編集経路の代替動線 + outline で色非依存（CSS）|
| NFR 5.1 可観測性 | bulk-tag の既存構造化ログ粒度を維持（トークン等の生値非出力）|

## 検証コマンドと結果

- `go test ./...` — 全 PASS（`internal/ui` に `TestDragTagSSRContract` 追加）
- `node --test static/*.test.mjs` — 177 pass / 0 fail（baseline 163 + 新規 14）
- `node --test extension/sidepanel.test.mjs` — 30 pass / 0 fail（拡張機能非回帰）
- `golangci-lint run` — 0 issues
- `go build ./...` — OK

## 確認事項（Reviewer / 人間に委ねる判断点）

1. **タッチ環境判定**: `matchMedia('(pointer: coarse)')` を採用。タッチ + マウス併用端末
   （一部 2-in-1）でも coarse 一致で trigger を表示するが、ドラッグ&ドロップも併用可能なので
   実害はない想定。判定方針の妥当性をレビューしてほしい。
2. **toast 文言**: 成功「タグを付与しました」/失敗「タグの付与に失敗しました」/タッチ案内
   「タグをタップして付与します」を直書き。i18n 方針が別途あれば調整余地あり。
3. **`succeeded[0]` 前提**: 単一 item_id 送信のため response も 1 件想定で `succeeded[0]` を
   参照。bulk-tag の単一 item 契約に依存しており、API 契約変更時は要追従。
4. **chip 再描画と #117 絞り込み状態**: 再描画 chip は `aria-pressed="false"` 固定で生成
   （付与直後に絞り込み選択中状態を引き継がない方針）。bulk_actions.js は URL から
   is-selected を算出するが、本 DnD では単一付与のため未算出。レビュー観点として共有。
5. design.md/tasks.md は本 Issue では未生成（単一実装パス）。spec 書き換えは行っていない。

STATUS: complete
