# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4.8 timestamp=2026-06-27T14:24:33Z -->

## Reviewed Scope

- Branch: claude/issue-120-impl-issue
- HEAD commit: 27036ce26721598231d4971cbf865a000deaa817
- Compared to: develop..HEAD
- Feature Flag Protocol: opt-out（flag 観点の確認は行わない）
- design.md / tasks.md は本 Issue では未生成（単一実装パス）。`_Boundary:_` アノテーションは
  存在しないため、Issue スコープ（/ui/items の UI 上のドラッグ&ドロップ・タッチ代替）を
  暗黙境界として評価した。

## Verified Requirements

- 1.1 — `templates/items_list.html` カードに `draggable="true" data-item-card` / `TestDragTagSSRContract`「Req 1.1」
- 1.2 — `templates/items.html` `.tag-filter-option` に `data-tag-drop-target` / `TestDragTagSSRContract`「Req 1.2 / 3.4」
- 1.3 — `items_drag_tag.js` `onDrop`→`assignTag`（bulk-tag POST）/ test「Req 1.3 / 1.4 / 2.1」（URL・body assert）
- 1.4 — `rebuildChipsForCard`（succeeded[0].tags で chip 再構築）/ test「Req 1.3 / 1.4 / 2.1」（chip assert）
- 1.5 — `onDrop` は drop target 不在で早期 return / test「Req 1.5」（fetch 0 件）
- 1.6 — drag ハンドラは classList と assignTag のみ操作し URL/filter/query/sort/page を一切変更しない（コード上で観測可能）+ test「NFR 2.2 / 2.3」
- 2.1 — 既存 `POST /v1/items/bulk-tag` で永続化 / test「Req 1.3」
- 2.2 — bulk-tag が DB 永続化（`handleBulkTagItems` 既存・テスト済）+ 成功時 chip 再描画
- 2.3 — bulk-tag additive/冪等 / test「Req 2.3 / 2.4」（go chip 1 件 assert）
- 2.4 — test「Req 2.3 / 2.4」（error 0 件 assert）
- 2.5 — fetch 先は bulk-tag 1 種のみ・server 変更なし（新規 API 追加なし）
- 2.6 — server `tag.Normalize`（既存）+ SSR の `data-tag-normalized` を送信
- 3.1 — `is-dragging` 付与 / test「Req 3.1」
- 3.2 — `is-drop-target` 付与（dragover preventDefault）/ test「Req 3.2 / 3.3」
- 3.3 — `clearDragVisuals`（dragleave / dragend）/ test「Req 3.2 / 3.3」
- 3.4 — `TestDragTagSSRContract`「Req 1.2 / 3.4」
- 3.5 — `templates/items.html` ボトムシート側にも同一ドロップ先契約 / test「Req 3.4 / 3.5」+ render_test（≥2 drop target / 空白含む名）
- 4.1 — `[data-card-tag-add]`（SSR hidden）+ `revealTouchTriggers`（coarse 検出時表示）/ test「Req 4.1」（coarse 表示 / 非 coarse 非表示）
- 4.2 — タッチ経路もドロップと同一 `assignTag` を共有 / test「Req 4.2 / 4.3」
- 4.3 — タッチ経路も bulk-tag 利用 / test「Req 4.2 / 4.3」（URL assert）
- 5.1 — 通信失敗 / failed[] 非空 / 非 200 時に chip 不付与 / test「Req 5.1 / 5.2」「Req 5.5」
- 5.2 — `toast.error` 通知 / test「Req 5.1 / 5.2」
- 5.3 — server 401（既存 bulk-tag 認可）→ 非 200 で UI 非反映+通知（test「Req 5.5」が failed/非反映経路を担保）
- 5.4 — server bulk-tag の所有権チェック（既存）→ failed 化 → UI 非反映（test「Req 5.5」）
- 5.5 — test「Req 5.5」（failed[] 返却時に chip 不付与 + error 通知 assert）
- NFR 1.1 / 1.2 — fetch 先 bulk-tag 1 種・request/response 契約は既存のまま（server 無変更）
- NFR 2.1 — JS 無効時は本ファイル未評価・トリガは `type="button"` + hidden で既存編集経路維持
- NFR 2.2 / 2.3 / 2.4 — 通常 click 非 intercept（drag イベントと tagging モード tap のみ扱う）/ test「NFR 2.2 / 2.3」
- NFR 3.1 / 3.2 — 同期 DOM class 付与（dragstart/dragover）+ 成功時 chip 即時再描画
- NFR 4.1 / 4.2 — タッチ代替 / 既存編集経路の代替動線 + outline/opacity で色非依存（`static/style.css`）
- NFR 5.1 — bulk-tag の既存構造化ログ粒度を維持（新規ログ追加なし・生値非出力）

## Findings

なし

## Summary

全 numeric AC（Req 1〜5）および NFR が、新規 `items_drag_tag.js` / SSR テンプレート契約と、
追加された単体テスト（`items_drag_tag.test.mjs` 14 件 / `TestDragTagSSRContract` 6 サブテスト、
いずれもローカルで green）でカバーされている。新規 API は追加されず既存 bulk-tag のみを利用、
server/migration/extension への変更はなく Issue スコープ内に収まり boundary 逸脱なし。

RESULT: approve
