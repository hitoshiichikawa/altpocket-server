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
  検索/絞り込み/状態タブ/ページ送りは `region.innerHTML` を差し替えて新カードを初期 hidden で
  再描画するため、`[data-items-region]` を `MutationObserver`（`items_bulk_selection.js` と同じ
  region 監視規約）で観測し、fragment 再描画のたびに trigger を再表示する（Req 4.1 / NFR 2.1）。
- **(c) 視覚フィードバック**: 最小限の class 付与 — `.is-dragging`（カード半透明/Req 3.1）、
  `.is-drop-target`（タグ要素に outline + 背景/Req 3.2）、`.is-tag-target`（tagging モード対象
  カードに outline/Req 4.1）、`.is-tagging`（付与処理中カードに opacity + progress カーソル +
  `aria-busy` を **drop/tap 直後に同期付与**し処理開始を 300ms 以内に提示/NFR 3.1）。色のみに
  依存せず outline/opacity で区別（NFR 4.2）。

## 変更ファイル

- `templates/items_list.html` — カードに `draggable="true"` / `data-item-card`、actions に
  `button[data-card-tag-add]`（初期 hidden）を追加。
- `templates/items.html` — サイドバー/ボトムシートの `.tag-filter-option` に
  `data-tag-drop-target` / `data-tag-name` / `data-tag-normalized` を付与。`items_drag_tag.js`
  を defer で読み込み。
- `static/items_drag_tag.js` — DnD + タッチ代替の実装（IIFE + `init()` + `_debug`、document
  delegated、auto-init skip flag）。chip 再構築は `items_bulk_actions.js` の SSR contract と一致
  し、`computeActiveNormalizedNames()` を移植して URL から `is-selected`/`aria-pressed` を算出。
  bulk-tag には display 名（`data-tag-name`）を送信。fragment 再描画は `MutationObserver` で
  trigger 再表示、付与中は `.is-tagging`/`aria-busy` を同期付与。
- `static/items_drag_tag.test.mjs` — 単体テスト 26 件（fake DOM + fetch stub + fake
  MutationObserver）。round 2 で 22→26（Filters ボタン経由のタッチ付与 / 無関係 tap での
  モード解除 / 連続付与時の stale レスポンス上書き防止 / 外部テキスト drop の弾き）。
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
| 2.6 同一タグ正規化 | server `tag.Normalize`（既存）。bulk-tag は受信文字列を display 名として保持しつつ dedup/空判定に正規化を適用するため、SSR の `data-tag-name`（display 名）を送る（#115 の display 名保持契約に合わせ既存タグ表示名の劣化を防ぐ）|
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
| 5.3 セッション失効時は変更しない | server 401/403/500 等の非 200 → 非反映+通知（drag-tag.test「Req 5.3」(401) /「Req 5.x」(403/500) が `items_drag_tag.js` の非 200 分岐を直接担保 + 「Req 5.5」が 200+`failed[]` 経路を担保）|
| 5.4 所有アイテム限定 | server bulk-tag が所有権で collapse（既存）→ failed → 非反映（drag-tag.test「Req 5.5」）|
| 5.5 非所有 id で変更せず通知 | drag-tag.test「Req 5.5」 |
| NFR 1.1/1.2 既存 EP のみ/契約不変 | fetch 先 bulk-tag 1 種・request/response 形は既存契約のまま（変更なし）|
| NFR 2.1 プログレッシブエンハンスメント | JS 無効時は本ファイル未評価・既存編集経路維持（型 `type=button`/SSR fallback）|
| NFR 2.2/2.3/2.4 #117/#118/#115 非回帰 | drag-tag.test「NFR 2.2 / 2.3」+ 通常 click 非 intercept・全既存テスト 177 pass |
| NFR 3.1/3.2 レスポンス品質 | drop/tap 直後に対象カードへ busy 状態（`.is-tagging` + `aria-busy`）を**同期付与**し処理開始を 300ms 以内に提示（drag-tag.test「NFR 3.1」）+ 成功時 chip 即時再描画（fetch 完了後）|
| NFR 4.1/4.2 a11y | タッチ代替/既存編集経路の代替動線 + outline で色非依存（CSS）|
| NFR 5.1 可観測性 | bulk-tag の既存構造化ログ粒度を維持（トークン等の生値非出力）|

## 検証コマンドと結果

- `go test ./...` — 全 PASS（`internal/ui` に `TestDragTagSSRContract` 追加）
- `node --test static/*.test.mjs` — 189 pass / 0 fail（baseline 163 + drag-tag 26。PR #143
  iteration round 1 で 14→22: display 名送信 / #117 選択状態維持 / fragment 再描画での touch
  trigger 再表示 / busy 同期フィードバック / 非 200 (401/403/500) 分岐の直接検証。round 2 で
  22→26: Filters ボタン経由のタッチ付与 / 無関係 tap でのモード解除 / 連続付与時の stale
  レスポンス上書き防止 / 外部テキスト drop の弾き。いずれも修正前に fail することを確認済み）
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
4. **chip 再描画と #117 絞り込み状態**（PR #143 iteration round 1 で修正済み）: 当初は
   再描画 chip を `aria-pressed="false"` 固定で生成していたが、`?tag=go` で絞り込み中の
   タグへ再ドロップすると SSR では選択状態だった chip が未選択に劣化し #117 非回帰
   （NFR 2.2）に反するため、`items_bulk_actions.js` と同一の `computeActiveNormalizedNames()`
   を移植し URL から `is-selected`/`aria-pressed` を算出して維持するよう変更した。
5. design.md/tasks.md は本 Issue では未生成（単一実装パス）。本 PR は実装 PR のため、
   spec（requirements.md / design.md / tasks.md）の追加・書き換えは行っていない。
   design.md/tasks.md の生成は Architect（設計 PR ゲート）の責務であり、実装 PR で
   混在させない方針（CLAUDE.md「1 PR = design or impl」）。

## PR #143 iteration round 2 で対応したレビュー指摘

1. **[high] タッチ代替がモバイル実 UI でタグ一覧に到達できない**（`items_drag_tag.js` の
   onClick 解除）: モバイルではタグ一覧がボトムシート内にあり、ユーザーは trigger tap →
   Filters ボタン（`[data-sheet-toggle]`）tap でシートを開く → タグ tap の順で操作する。
   従来は中間の Filters ボタン tap で `exitTaggingMode()` され `pendingTouchItemId` が消えて
   いた。`shouldPreserveTaggingMode()` を追加し、シート開閉トグル / `.sheet-overlay` /
   `.sidebar` / `.tag-list` 内の tap ではモードを維持、無関係 tap でのみ解除するよう変更
   （Req 4.1 / 4.2）。回帰防止に「Filters ボタン経由のタッチ付与」「無関係 tap での解除」の
   2 テストを追加。
2. **[medium] 連続付与時の stale レスポンス上書き**（`assignTag`）: 同一カードへ複数タグを
   短時間にドロップすると複数 `assignTag` が in-flight になり、古いレスポンスが後着すると
   `succeeded[0].tags` の部分集合で chip 列を巻き戻し、永続化済みの別タグを UI 上から消して
   いた。item ごとの単調増加世代（`tagAssignGenerations`）を導入し、最新世代のレスポンスで
   のみ chip 再構築 / busy 解除を行うよう変更（Req 1.4 / NFR 3.2）。bulk-tag は additive で
   サーバ永続化は壊れないため、UI 巻き戻しのみの修正。回帰防止テストを追加。
3. **[low] 外部テキスト drop / セレクタ汚染**（`onDrop` / `findCardByID`）: `findCardByID` を
   動的属性セレクタ組み立てから `[data-item-card]` の線形探索に変更し、引用符等を含む値で
   `querySelector` が SyntaxError を投げる経路を排除。さらに onDrop で region 内の実在カードに
   紐づく id のときだけ付与に進めるよう gate を追加（カード以外の外部テキストを弾く）。
   回帰防止テストを追加。
4. **[medium] タッチ代替テストが実モバイル経路を通っていない**: 上記 1 の修正に合わせ、
   trigger → Filters ボタン → タグ tap の実経路テストを追加（修正前 fail を確認）。
5. **[medium] design.md / tasks.md 不在による追跡不能**: 本 Issue は needs_architect:false で
   triage され design.md/tasks.md が未生成。本 PR は実装 PR のため spec の追加・書き換えは
   workflow 規約（CLAUDE.md「1 PR = design or impl」「Developer は実装 PR で
   requirements/design/tasks を書き換えない」）により行わない。requirements⇄実装の追跡は
   本 impl-notes の「AC ↔ テスト対応表」が担う。design/tasks の追跡が必要なら別途
   needs_architect:true での設計 PR ゲートを推奨（返信で提起）。

STATUS: complete
