# Implementation Plan

本 spec のタスクは以下の依存順で実装する。Web UI が重い責務は「タブ SSR」「カードボタン
SSR」「JS 状態切替」「JS タブ切替」を別タスクに分割し、各タスクが `DEV_MAX_TURNS=60` 以内に
収まるようにした。

- [ ] 1. マイグレーション 007: items.status カラム追加と backfill
  - `migrations/007_add_item_status.sql` を新規作成
  - `ALTER TABLE items ADD COLUMN status TEXT NOT NULL DEFAULT 'unread'`
  - `ALTER TABLE items ADD CONSTRAINT items_status_check CHECK (status IN ('unread', 'read', 'archived'))`
  - `CREATE INDEX items_user_status_idx ON items (user_id, status, created_at DESC)`
  - 冪等性のため `IF NOT EXISTS` を可能な箇所で利用し、Issue #119 の意図・background・既存
    マイグレーション 001..006 との相対適用順をファイル冒頭コメントに記述
  - 既存マイグレーション（001〜006）の中身は **書き換えない**
  - _Requirements: 1.1, 1.2, 1.3, 1.5, 6.1_
  - _Boundary: migrations_

- [ ] 2. store 層: Item.Status / 状態定数 / UpdateItemStatus / ListItems 拡張
  - `internal/store/store.go`:
    - `Item` 構造体に `Status string \`json:"status"\`` を追加
    - 定数 `ItemStatusUnread = "unread"` / `ItemStatusRead = "read"` /
      `ItemStatusArchived = "archived"` を package-level で公開
    - 新規メソッド `UpdateItemStatus(ctx, userID, itemID, next string) (prev string, err error)`
      を実装（所有チェック + UPDATE + RETURNING old status / `pgx.ErrNoRows` で 404 collapse）
    - `ListItems` シグネチャを `(ctx, userID, page, perPage, q, tags, statuses, sort)` に
      拡張。`statuses` が非空なら `i.status = ANY($N)` を WHERE に追加。SELECT に `i.status`
      を含める
    - `GetItemDetail` の SELECT にも `i.status` を含める
  - `internal/store/mcp_recent.go`: `ListRecentItems(ctx, userID, since, statuses)` に
    `statuses` を追加し、非空なら同様に WHERE 追加
  - `internal/store/json_tags_test.go`: `ItemListRow` に `Status: "read"` を入れて
    `"status"` snake_case キーが出ることを確認するアサーションを追加
  - 既存呼び出し側（server / mcpserver / worker）の compile error は次タスク以降で順次解消する
    ため、本タスクではコンパイル成立は require しない（spec 内の単体差分のみ作成）
  - **テスト追加（同 task 内）**: 上記 `json_tags_test.go` の `status` snake-case 検証を
    本タスクで完結させる（NFR 6 / Req 1.1 に対する同 task 内テスト必須）
  - _Requirements: 1.1, 1.4, 1.6, 3.1, 3.3, 3.4, 3.5, 6.2, NFR 2.1, NFR 3.1_
  - _Boundary: Store_

- [ ] 3. store 層 integration test: UpdateItemStatus / ListItems status フィルタ / 007 backfill
  - `internal/store/store_item_status_test.go` を新規作成（`//go:build integration` tag）:
    - `TestUpdateItemStatus_TransitionsAllPairs`: 7 通り（unread↔read / unread↔archived /
      read↔archived / archived→unread / 既存値再設定）の遷移と `prev` 返り値を実 DB で確認
    - `TestUpdateItemStatus_RejectsOtherUserItem`: 他ユーザー所有 item で `pgx.ErrNoRows`
      が返ることを assert（NFR 2.1）
    - `TestUpdateItemStatus_RejectsInvalidStatus`: CHECK 制約による拒否を assert（Req 1.5
      二重防御）
    - `TestListItems_FilterByStatus`: 3 件作成 → `statuses=[unread]` / `[unread,read]` /
      `[archived]` / `nil` の各ケースで期待件数を確認
    - `TestListRecentItems_FilterByStatus`: 同上を `ListRecentItems` で
  - 既存 `items_active_filters_integration_test.go` の `newIntegrationStore` パターンと
    `seedItemsActiveFilterUser` パターンを参考にし、cleanup 規約に従う
  - _Requirements: 1.4, 1.5, 1.6, 3.3, 3.4, 3.5, 5.4, 6.1, NFR 2.1_
  - _Boundary: Store_
  - _Depends: 1, 2_

- [ ] 4. server 層: handleSetItemStatus / parseStatusFilter / handleListItems / handleUIItems 接続
  - `internal/server/server.go`:
    - `parseStatusFilter(q url.Values) []string` を追加（design.md の表通り。`""` / `unread` /
      `all` / `archived` / `read` / 不明値 のマッピング）
    - `handleSetItemStatus(w, r)` を追加: JSON `{"status":"<v>"}` を受理、enum 検証、`requireAuth`
      / `limiter` / CSRF は既存 middleware 経由、`Store.UpdateItemStatus` 呼び出し、成功時
      `slog.Info("items.status.update", user_id, item_id, prev, next, request_id)` を出力
    - `route("/v1/items", ...)` 配下に `r.Patch("/{id}/status", s.requireAuth(s.handleSetItemStatus))` を追加
    - `handleListItems` / `handleUIItems` で `statuses := parseStatusFilter(r.URL.Query())` を
      `s.store.ListItems(... statuses, sort)` に渡す
    - `handleUIItems` のテンプレート data に `"StatusTab"` / `"StatusTabURLs"` を追加（次タスクで
      テンプレート側を実装）
  - `internal/server/items_status_test.go` を新規作成:
    - `Test_parseStatusFilter_TableDriven`: `""` / `"unread"` / `"all"` / `"archived"` / `"read"` /
      不明値 / 大文字混在 → 期待 `[]string`
    - `TestHandleSetItemStatusUnauthorizedReturnsJSONError`
    - `TestHandleSetItemStatusInvalidJSONReturns400`
    - `TestHandleSetItemStatusInvalidStatusReturns400`（`{"status":"foo"}` → 400 invalid_status）
    - `TestHandleSetItemStatusEmptyStatusReturns400`
  - `extension_contract_test.go` は **変更しない**（成功時の JSON フィールド構造は assert
    していないため）
  - **テスト追加（同 task 内）**: 上記 5 種の handler / parser テストを本タスクで完結させる
    （Req 1.5 / NFR 2.1 / NFR 3.1 の同 task 内テスト必須カテゴリに該当）
  - _Requirements: 1.4, 1.5, 1.6, 2.3, 2.4, 2.5, 2.6, 3.1, 3.3, 3.4, 3.5, 3.6, NFR 2.1, NFR 3.1_
  - _Boundary: Server_
  - _Depends: 2_

- [ ] 5. mcpserver 層: status 引数 / status 出力フィールド / DataSource 拡張
  - `internal/mcpserver/deps.go`: `DataSource.ListItems` / `ListRecentItems` のシグネチャを
    store の新シグネチャに揃える（`statuses []string` 追加）
  - `internal/mcpserver/server.go`:
    - `ListItemsInput` / `SearchItemsInput` に `Status string \`json:"status,omitempty"\`` を追加
    - 内部ヘルパー `mcpStatusFilter(s string) []string` を追加（既定 `unread`、`unread/read/archived/all` を受理、不明値は `unread` フォールバック）
    - `listItemsHandler` / `searchItemsHandler` / `recentArticlesHandler` で `mcpStatusFilter(args.Status)`
      の結果を store に渡す
    - `formatItemList` / `getItemHandler` の出力 JSON に `"status": item.Status` / `"status": detail.Status` を追加
  - `internal/mcpserver/server_test.go`:
    - fake DataSource を更新（新シグネチャ）
    - `TestListItemsHandler_DefaultStatusIsUnread`: `Status` 空入力で store に `[unread]` が渡ることを assert
    - `TestListItemsHandler_AllReturnsUnreadAndRead`: `Status: "all"` → `[unread,read]`
    - `TestListItemsHandler_OutputContainsStatus`: 返却 JSON に `"status"` キーが含まれることを assert
    - `TestRecentArticlesHandler_DefaultStatusIsUnread`: 既定値の伝播を assert
  - **テスト追加（同 task 内）**: 上記 4 種の MCP handler テストを本タスクで完結させる
    （Req 5.1 / 5.2 / 5.3 / 6.3 の同 task 内テスト必須）
  - _Requirements: 5.1, 5.2, 5.3, 5.4, 6.3, NFR 2.2_
  - _Boundary: McpServer_
  - _Depends: 2_

- [ ] 6. SSR テンプレート: 状態タブ markup + items_list の data-status / status-badge
  - `templates/items.html`:
    - 検索バー直下、`<section class="split">` の手前に `<nav class="status-tabs" role="tablist"
      aria-label="アイテム状態">` を追加。Unread / All / Archived の 3 タブを `<a role="tab"
      aria-selected="..." href="{{index .StatusTabURLs "unread"}}">` 形式で描画
    - active タブには `class="is-active"` と `aria-selected="true"` を付与（`{{if eq .StatusTab "unread"}}...{{end}}`）
  - `templates/items_list.html`:
    - `<article class="tile item-card {{if eq .FetchStatus "failed"}}failed{{end}}"
      data-status="{{.Status}}" ...>` のように `data-status` を追加
    - meta 行に `<span class="item-status-badge" data-status="{{.Status}}" role="status"
      aria-label="状態: {{.Status}}">{{.Status}}</span>` を追加（NFR 4: テキストラベル併用）
  - SSR でタブの aria-selected と URL クエリの整合性を取れることをハンドラ側 data 渡しで確認
    （server タスク 4 の `StatusTabURLs` / `StatusTab` data 整備済み前提）
  - JS 無効環境でもタブが `<a href>` でフルページ遷移として動作することを目視確認
  - **テスト追加（同 task 内）**: テンプレート差分の単体テストは Go 側の既存 renderer test の
    枠を使わず、次タスク 7 のボタン追加・タスク 9 のスタイル追加と合わせた目視確認に統一する
    （SSR テンプレートのみの単独 regression test は本リポジトリで歴史的に低価値のため省略）
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 4.1, 4.4_
  - _Boundary: Templates_
  - _Depends: 4_

- [ ] 7. SSR テンプレート: item-card の既読/アーカイブボタン追加（archive 解除も含む）
  - `templates/items_list.html` の `.item-actions` 内に以下を追加:
    - `<button type="button" class="btn-secondary mark-read-toggle" data-item-id="{{.ID}}"
      data-current-status="{{.Status}}" aria-label="{{if eq .Status "read"}}未読に戻す{{else if eq .Status "archived"}}未読に戻す{{else}}既読にする{{end}}">{{if eq .Status "read"}}Mark unread{{else if eq .Status "archived"}}Mark unread{{else}}Mark read{{end}}</button>`
    - `<button type="button" class="btn-secondary archive-toggle" data-item-id="{{.ID}}"
      data-current-status="{{.Status}}" aria-label="{{if eq .Status "archived"}}アーカイブ解除{{else}}アーカイブする{{end}}">{{if eq .Status "archived"}}Unarchive{{else}}Archive{{end}}</button>`
  - `templates/item_detail.html` の actions 列にも同じ 2 ボタンを追加（任意・同 PATCH 経路を共有）
  - Tab フォーカス順序が既存ボタン（Original / Refetch / Delete）と整合することを目視確認
  - **テスト追加（同 task 内）**: テンプレートのみの単独 Go test は既存規約上追加せず、次タスク
    8 の JS テストと組み合わせて statictest（`extension/sidepanel.test.mjs` と同じ
    `node --test` 系）でカバーする方針を本タスク内で明示。本タスクは markup 追加のみ
  - _Requirements: 2.1, 2.2, 2.6, NFR 4.1, NFR 4.2_
  - _Boundary: Templates_
  - _Depends: 6_

- [ ] 8. static JS: 状態切替ボタンの delegated click + 失敗時巻き戻し
  - `static/app.js` の既存 delegated click handler（refetch / delete の隣）に追加:
    - `button.mark-read-toggle`: `currentStatus` を読み、`next = currentStatus === 'read' ? 'unread' : 'read'`
      （`archived` なら `unread`）を算出。`fetch('/v1/items/' + id + '/status', {method:'PATCH',
      headers: {...headers, 'Content-Type':'application/json'}, body: JSON.stringify({status: next})})` を呼ぶ
    - 成功時: card の `data-status` 属性を更新、ボタンの label / aria-label を新状態に合わせて
      書き換え、`item-status-badge` のテキストを更新。現在の status タブ条件で非表示にすべき
      item は `<article>` 要素を fade-out で DOM 削除（Req 2.8）
    - 失敗時: `toast.error('状態の更新に失敗しました')` + ボタンと card の元状態維持（Req 2.7）
    - `button.archive-toggle`: 同様、`next = currentStatus === 'archived' ? 'unread' : 'archived'`
  - 既存 `app.js` の keyboard shortcut handler は変更しない（設計確認事項 (c) により本 Issue
    では新規 shortcut なし）
  - **テスト追加（同 task 内）**: `static/items_status.test.mjs` を新規追加し、`node --test`
    で以下を検証（既存 `items_active_filters.test.mjs` のパターンを参考にする）:
    - mark-read-toggle click で fetch が `/v1/items/<id>/status` PATCH を呼ぶ
    - 成功時に `data-status` が更新される
    - 失敗時に元の `data-status` が維持される
    - archive-toggle click で next が `archived`、archived 時は `unread` になる
  - _Requirements: 2.3, 2.4, 2.5, 2.6, 2.7, 2.8, NFR 1.3_
  - _Boundary: Static_
  - _Depends: 4, 7_

- [ ] 9. static JS: items_status.js（タブ切替 + fragment 取得 + popstate）
  - `static/items_status.js` を新規作成。`static/items_active_filters.js` の pattern に揃える:
    - `[data-items-region]` 上の `__itemsFragmentInflight` slot を共有して AbortController を持つ
    - `<nav.status-tabs a[role="tab"]>` の click を delegated 捕捉 → `?status=` を書き換えた
      相対 URL を計算 → `history.pushState` → `X-Requested-With: ItemsFragment` で fragment 取得
    - popstate で `?status=` を読み取って fragment 取得（Req 3.8 の URL クエリ永続を戻る/進むに追従）
    - 修飾キー付き click（Cmd/Ctrl/Shift/Alt）は intercept せず既定動作を維持
  - `templates/items.html` の script 読み込み行に `<script src="/static/items_status.js?v={{assetVersion}}" defer></script>` を追加
  - **テスト追加（同 task 内）**: `static/items_status_tabs.test.mjs` を新規追加し、`node --test`
    で以下を検証:
    - タブ click で URL が `?status=...` に切り替わる
    - fragment fetch が `X-Requested-With: ItemsFragment` を含む
    - popstate で fragment 再取得が起きる
    - 連続切替時に AbortController で前段が abort される（race 防止）
  - _Requirements: 3.2, 3.6, 3.7, 3.8, NFR 1.1, NFR 1.2_
  - _Boundary: Static_
  - _Depends: 6_

- [ ] 10. CSS: 状態タブ / data-status カード / status-badge スタイル + #12 との非衝突確認
  - `static/style.css`:
    - `.status-tabs` をルートに追加（`.active-filters` と同じ余白トークン）。`a[role="tab"]`
      の通常 / hover / aria-selected="true" の 3 状態
    - `.item-card[data-status="read"]`: タイトル色を `--text-tertiary` トーン化、`opacity: 0.85`
      程度の弱化。border-left は使わない（#12 の `.failed` と衝突しないため）
    - `.item-card[data-status="archived"]`: 背景を `--bg-elevated` から弱化、左側に細い
      点線インジケータ等で archived を視覚化（border-left は使用しない）
    - `.item-status-badge[data-status="unread"]` / `[read]` / `[archived]`: status-pill と同様の
      丸ドット + テキスト併用スタイル。色覚多様性に配慮するため、必ず **ドット + テキストラベル**
      を併記する（NFR 4 の色のみ依存禁止）
    - `.item-card.failed` と `.item-card[data-status="archived"]` が同時に成立しても破綻
      しないことを確認（failed の border-left + archived の背景弱化が共存）
  - light / dark テーマ両方で視覚区別が成立することを目視確認
  - **テスト追加（同 task 内）**: CSS のみのタスクのため、視覚回帰テストは本リポジトリの既存
    pattern上手動目視で確認する（既存 #12 / #115 / #117 と同じ運用）。Go test での追加は不要
  - _Requirements: 4.1, 4.2, 4.3, 4.4_
  - _Boundary: Static_
  - _Depends: 6, 7_

## Verify

本 spec の実装後、watcher（stage-a-verify gate）が再実行すべき verify コマンドを以下の
構造化ブロックで宣言する。Go test と golangci-lint と拡張機能テストの 3 系統を順次実行する。

<!-- stage-a-verify -->
```sh
go test ./... && golangci-lint run && node --test static/items_active_filters.test.mjs static/items_search.test.mjs static/items_tags.test.mjs static/items_fragment_race.test.mjs
```
