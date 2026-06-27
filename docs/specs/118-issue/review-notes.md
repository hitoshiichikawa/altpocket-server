# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-06-27T00:00:00Z -->

## Reviewed Scope

- Branch: claude/issue-118-impl-issue
- HEAD commit: 943a834457448b72172ef987c0d24ba055c05c9a
- Compared to: develop..HEAD
- Commits: 24（task 1〜8 の実装 / docs(tasks) mark / docs(impl-notes) 各 1 件ずつ）
- Files changed: 16 (約 +7394 行 / -10 行)

## Verified Requirements

### Requirement 1 (アイテム選択 UI)
- 1.1 — `templates/items_list.html` 各 `<article>` 内に `<input type="checkbox" class="item-select">` 挿入 / `static/items_bulk_selection.js` `onChange` で toggle
- 1.2 — `static/items_bulk_selection.js:onChange` (`willCheck === true` → `addToSet`) / `TestSingleCheckboxToggle`
- 1.3 — 同 `onChange` の `willCheck === false` 経路 / `TestUncheckRemoves`
- 1.4 — `static/style.css` `.item-card.is-selected { background: ...; box-shadow: inset ... }` + チェックボックス checked 状態併用
- 1.5 — checkbox は色だけでなく形状・checked 状態で識別可能（aria-label 付き）
- 1.6 — 新規モジュールは既存 `items_status.js` / `items_active_filters.js` / `items_tags.js` / `items_search.js` のいずれにも干渉しない（独立 `document.addEventListener` / 既存 DOM 構造を一切改変せず）

### Requirement 2 (Shift+クリック範囲選択)
- 2.1 — `onClick` の 3 条件 (`size > 0` && `lastClickedID !== null` && anchor 存在) + `computeRange` / `TestShiftClickRange`
- 2.2 — `addToSet` 経路で既選択は `if (selectionSet.has(rid)) continue;` で skip / `TestShiftClickPreservesExistingSelection`
- 2.3 — `computeRange` が region 配下の `.item-card` の DOM 順で範囲を算出 + shift+click ごとに `lastClickedID = id` 更新 / `TestShiftClickUpdatesLastClickedAnchor`
- 2.4 — 3 条件のいずれかが満たされない場合は単一 toggle に降格 / `TestShiftClickWithoutHistoryActsAsSingleToggle` / `TestShiftClickWithEmptySelectionActsAsSingleToggle` / `TestShiftClickWithStaleAnchorActsAsSingleToggle`

### Requirement 3 (選択ツールバー)
- 3.1 — `templates/items.html` `.bulk-toolbar[data-bulk-toolbar]` SSR + `items_bulk_actions.js:onBulkSelectionChanged` で `count > 0` → `showToolbar(count)` / `TestToolbarShowsHidesOnSelectionChange`
- 3.2 — 同 handler の `count === 0` → `hideToolbar` / 同テスト
- 3.3 — toolbar SSR に「一括削除」「一括タグ付け」「選択解除」3 button を含む
- 3.4 — `onToolbarClick:bulk-clear` → `selection.clear()` / `TestClearButtonCallsSelectionClear`
- 3.5 — `static/style.css` `.bulk-toolbar { position: sticky; bottom: 0; z-index: 300; }`
- 3.6 — `dispatchChanged()` を toggle / 範囲 / clear / removeFromSelection / fragment swap reset / popstate の全パスで発火

### Requirement 4 (一括削除)
- 4.1 — `items_bulk_actions.js:showConfirm('一括削除', requestIds.length + ' 件を削除しますか？', ...)` / `TestDeleteButtonShowsConfirm` / `TestDeleteConfirmUsesShowSignature`
- 4.2 — `window.altpocketConfirm.show()` の cancel/Escape 経路で `onConfirm` は発火しない (既存 `app.js` 動作) / `TestDeleteCancelDoesNothing`
- 4.3 — cancel 時に `performBulkDelete` が呼ばれず selection 保持 / `TestDeleteCancelDoesNothing`
- 4.4 — `internal/store/items_bulk.go:BulkDeleteItems` `DELETE FROM items WHERE id = ANY($1::uuid[]) AND user_id = $2 RETURNING id` / store integration test `TestBulkDeleteItems_DeletesOwnAndIgnoresOthers` / `TestDeleteConfirmCallsAPI`
- 4.5 — `performBulkDelete` の 200 OK 経路で succeeded 各 id を `fadeOutAndRemove` / `TestDeleteAllSuccessRemovesCardsAndDeselectsSnapshot`
- 4.6 — 全成功時に `selection.removeFromSelection(requestIds)` → toolbar hide (`bulkselection:changed` event 経由) / 同テスト
- 4.7 — `showBulkFailureDialog({verb: '削除', items: ...})` で title/url を `<li>` に全件 populate / `TestDeletePartialFailureKeepsFailedSelected` / `TestDeleteRateLimitedShowsFailureDialog` / `TestDeleteServerErrorShowsFailureDialog` / `TestFailureDialogPopulatesAllItemsWithoutTruncation` (100 件まで)
- 4.8 — 部分失敗時に succeeded のみ `removeFromSelection` で外し failed は selection に残置 / `TestDeletePartialFailureKeepsFailedSelected` / server integration `TestHandleBulkDeleteItems_PartialFailureResponse`

### Requirement 5 (一括タグ付け)
- 5.1 — `<dialog data-bulk-tag-dialog>` + `<input data-bulk-tag-input>` SSR / `TestTagButtonOpensDialog`
- 5.2 — server `handleBulkTagItems` が既存 `normalizeTagInputs` を流用 / store `BulkAddItemTag` が `tagInput.Name` (display) と `tagInput.NormalizedName` を分離保持
- 5.3 — `internal/store/items_bulk.go:BulkAddItemTag` step 4 `INSERT INTO item_tags SELECT id, $1, $2 FROM unnest($3::uuid[])` / `TestBulkAddItemTag_AddsToOwnedOnlyAndDedupes`
- 5.4 — 同 INSERT の `ON CONFLICT (item_id, tag_id) DO NOTHING` / `TestBulkAddItemTag_AddsToOwnedOnlyAndDedupes` / server integration `TestHandleBulkTagItems_DedupesExistingTagInRequest`
- 5.5 — `BulkAddItemTag` step 5 が FULL post-update tag set を返却 → `performBulkTag` の `rebuildChipsForCard` で chip 列再構築 / `TestTagSuccessRebuildsChipsWithFilterToggleContract` / store integration `TestBulkAddItemTag_ReturnsFullTagListPerItem`
- 5.6 — 全成功で `selection.removeFromSelection(requestIds)` + `tagDialog.close()` / `TestTagSuccessDeselectsSnapshotAndClosesDialog`
- 5.7 — `showBulkFailureDialog({verb: 'タグ付け', items: ...})` / `TestTagPartialFailureKeepsFailedSelected` / `TestTagRateLimitedShowsFailureDialog` / `TestTagServerErrorShowsFailureDialog`
- 5.8 — 部分失敗で succeeded のみ removeFromSelection、failed は selection 残置 / `TestTagPartialFailureKeepsFailedSelected`
- 5.9 — `onTagFormSubmit` の `if (!normalized)` で空判定 → no-op + input focus 戻し / server 側 `tag.Normalize(req.Tag) == ""` → 400 invalid_tag / `TestTagDialogEmptyInputIsNoOp` / `TestHandleBulkTagItems_EmptyTagReturns400InvalidTag` / `TestHandleBulkTagItems_NormalizationEmptyTagReturns400InvalidTag`

### Requirement 6 (キーボードショートカット)
- 6.1 — `items_bulk_selection.js:onKeydown` で `e.key === 'x'` + `activeElement.closest('.item-card')` で toggle / `TestKeyboardXTogglesFocusedCard`
- 6.2 — `x` キーは既存 `j` / `k` / `o` / `n` / `/` / `?` / `e` (app.js) と衝突しない + 本モジュールは modifier present で return / `TestKeyboardXIgnoresModifierCombo`
- 6.3 — TEXTAREA / SELECT / contenteditable / 文字入力 INPUT (TEXT_INPUT_TYPES) で return / `TestKeyboardXIgnoresInputFocus`
- 6.4 — toolbar は SSR で `<button type="button">` (Tab フォーカス可能) / dialog 内 button 群も native button
- 6.5 — toolbar button は native `<button>` で Tab → Enter / Space がブラウザ標準で click を発火する（actions モジュールは別途 keydown を捕捉しないため標準動作）

### Requirement 7 (選択状態のライフサイクル)
- 7.1 — MutationObserver `addedNodes.length > 0` (fragment 差替) で `selectionSet.clear()` + `dispatchChanged()` / `TestFragmentSwapResetsSelection`
- 7.2 — 同 MutationObserver 経路 (タブ・チップ・検索・ソート・ページ送りはすべて `[data-items-region].innerHTML` 置換に集約)
- 7.3 — init 時に `selectionSet.clear()` + `lastClickedID = null` (fresh pageload は新 IIFE 起動) / `TestInitialStateIsEmpty`
- 7.4 — `win.addEventListener('popstate', onPopState)` で Set クリア / `TestPopstateResetsSelection`
- 7.5 — MutationObserver 経路と同じ (`addedNodes.length > 0` の per-record 判定が bracket 中であっても reset を発火) / `TestFragmentSwapDuringActionBracketStillResets` / `TestEndActionMutationProcessesQueuedFragmentSwapBeforeDiscard`

### Requirement 8 (認可・データ分離)
- 8.1 — store SQL 全てが `WHERE user_id = $2` または `EXISTS (SELECT 1 FROM items WHERE ... user_id = $2)` で締め / `BulkAddItemTag` step 1 の `SELECT ... FOR KEY SHARE WHERE user_id = $2` / store integration `TestBulkDeleteItems_DeletesOwnAndIgnoresOthers` / `TestBulkAddItemTag_AddsToOwnedOnlyAndDedupes` / server integration `TestHandleBulkDeleteItems_PartialFailureResponse`
- 8.2 — 他ユーザー所有 ID は SELECT 段階で除外 → failed[{reason: "not_found"}] に collapse / `TestBulkAddItemTag_PartialFailureFromOtherUserID` / `TestHandleBulkTagItems_PartialFailureFromOtherUserID` / `BulkAddItemTag` step 2 EARLY RETURN ガードで tags 行副作用も防ぐ (`TestBulkAddItemTag_AllNotOwnedDoesNotCreateTagsRow`)
- 8.3 — 削除済み id も SELECT 段階で除外 + invalid UUID は handler `partitionByUUID` で store 前に collapse / `TestBulkDeleteItems_PartialFailureFromMissingID` / `TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound_FakeStore` / server integration `TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound`

### NFR 1 (レスポンス品質)
- 1.1 — `onChange` / `addToSet` / `dispatchChanged` は同期処理 (event 発火 → toolbar 即時更新)
- 1.2 — handler 側で fade-out 300ms + `removeFromSelection` 同期発火（100 件以下では実 DB latency が支配的だが構造的に 1 秒内収束する設計）
- 1.3 — `performBulkDelete` / `performBulkTag` は対象 article のみを `article.remove()` し、items-list 全体 innerHTML を書き換えない（NFR 1.3 ちらつき防止）

### NFR 2 (範囲・上限)
- 2.1 — JS 側 `MAX_SELECTION = 100` + server 側 `maxBulkItemsPerRequest = 100` の二重防御 + 16KiB body cap (`maxBulkRequestBodyBytes`) / `TestHandleBulkDeleteItems_OverLimitReturns400PayloadTooLarge` / `TestHandleBulkDeleteItems_RequestBodyExceedsByteLimitReturns400PayloadTooLarge`
- 2.2 — 単一 toggle / range 双方で上限 enforcement + toast.error / `TestUpperLimitRejectsBeyond100` / `TestShiftRangeAcrossUpperLimitRejectsEntireRange`

### NFR 3 (後方互換性)
- 3.1 — 既存 status-tabs (Issue #119) markup 不変、CSS selector の追加のみ
- 3.2 — 既存 active-filters chip (Issue #115) markup 不変、JS は legacy `?tags=csv` 形式も解釈 / `TestTagSuccessRespectsLegacyTagsCsvParam`
- 3.3 — 既存 tag chip (Issue #117) markup 不変、chip rebuild は SSR contract と完全一致 / `TestTagSuccessRebuildsChipsWithFilterToggleContract` / `TestTagSuccessPreservesActiveFilterChipSelectedState`
- 3.4 — `extension_contract_test.go` を改変せず、単一アイテム handler (`handleDeleteItem` / `handleUpdateItemTags` 等) も不変
- 3.5 — `<input type="checkbox" disabled>` SSR + JS 有効時のみ `removeAttribute('disabled')` で Progressive Enhancement / `TestProgressiveEnhancementRemovesDisabled` / `TestFragmentSwapReEnablesNewDisabledCheckboxes`

### NFR 4 (アクセシビリティ)
- 4.1 — checkbox / toolbar button / dialog 内 button / tag input すべて native HTML elements で Tab + Enter/Space 操作可能
- 4.2 — `aria-label="アイテムを選択: {{.Title}}"` / `role="region" aria-label="一括操作"` / `aria-labelledby` / `aria-describedby` / `role="alertdialog"` を SSR
- 4.3 — `.item-card.is-selected` は背景色 + inset box-shadow + checkbox checked 状態の併用（色のみではない）

### NFR 5 (可観測性)
- 5.1 — `slog.Info("items.bulk.delete", user_id, item_ids, succeeded_count, failed_count, failed_ids, request_id)` / 同じく `items.bulk.tag` + `tag_normalized` / Cookie / Authorization / body raw は出力しない / `TestHandleBulkDeleteItems_LogsStructuredFields_FakeStore` / `TestHandleBulkTagItems_LogsStructuredFields_FakeStore` / server integration `TestHandleBulkDeleteItems_LogsStructuredFields` / `TestHandleBulkTagItems_LogsStructuredFields`

### Boundary 適合
- 全ファイル変更は tasks.md の `_Boundary:_` 値 (`Store` / `Server` / `Templates` / `Static`) と一致
- `static/app.js` への 2 行追加は tasks.md task 7 (line 704-721) で明示的に許可された範囲 (`window.altpocketConfirm` / `window.altpocketNormalizeTagName` の公開)
- `internal/server/server.go` への 3 箇所追加 (struct field + New() 代入 + Route 登録 2 行) は tasks.md task 3 (line 142-146 + 199-202) で明示
- 既存 handler / `extension_contract_test.go` / 既存テンプレ markup / 既存 CSS selector の改変はゼロ (NFR 3.1〜3.5 維持)

### テスト実行確認
- `go test ./...`: 全 14 パッケージ pass (`internal/server` / `internal/store` を含む)
- `node --test extension/sidepanel.test.mjs static/items_bulk_selection.test.mjs static/items_bulk_actions.test.mjs`: 85 件 pass (extension 21 + selection 21 + actions 43)
- store / server integration test は `//go:build integration` tag 付きで CI スコープ外（開発者ローカル / Reviewer 手元の実 DB 経路で担保される）— tasks.md verify block の `## Verify` 節と運用方針が一致

## Findings

なし

## Summary

per-task ループで 8 タスクすべてが完了し、impl-notes.md には各タスクで採用された設計判断・残存課題（task 8 の light/dark 目視のみ未確認、後続単体タスク不要）が記録されている。requirements.md の Requirement 1〜8 / NFR 1〜5 のすべての numeric AC について、対応する実装またはテスト（unit / integration / SSR / JS）が確認できた。tasks.md の `_Boundary:_` 制約に違反する変更はなく、`static/app.js` への 2 行追加と `internal/server/server.go` の wiring 拡張はいずれも tasks.md で明示的に許可された範囲内。`go test ./...` + 85 件の Node.js テストはすべて pass。

RESULT: approve
