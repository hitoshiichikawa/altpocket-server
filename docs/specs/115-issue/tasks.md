# Implementation Tasks — Issue #115 アクティブフィルタチップ表示

> 本 tasks.md は実装 PR #137 の codex レビューで判明したマルチテナント表示名漏洩の
> リデザイン分を中心に、要件 1〜6 / NFR を実在 AC ID へトレースする。チップ列レンダリングと
> JS 操作（タスク 4 / 5）は実装済みで、本リデザインでは不変。

- [x] 1. per-user 表示名のスキーマ移設（migration）
  - `item_tags` に `display_name TEXT NOT NULL DEFAULT ''` を追加する migration を新規番号
    `006` で追加（`ADD COLUMN IF NOT EXISTS` で冪等）
  - 既存データを共有 `tags.name` から `item_tags.display_name` へ backfill（`display_name = ''`
    の行のみ対象＝再適用安全 / 導入前の見た目を初期値として維持）
  - forward-only（down 不要、既存 migration は不変）
  - _Requirements: 1.3, 5.4, 4.5_

- [x] 2. 書き込み経路を per-user 表示名へ（store write）
  - `upsertItemTags` ヘルパを追加し、共有 `tags` を `normalized_name` で upsert（no-op conflict、
    `tags.name` は上書きしない）+ `item_tags(item_id, tag_id, display_name)` を
    `ON CONFLICT (item_id, tag_id) DO UPDATE SET display_name=EXCLUDED.display_name` で upsert
  - `CreateItem` / `PatchItem`（→ `ReplaceItemTags`）の両 write 経路を `upsertItemTags` に統一し、
    既存共有行と衝突しても入力表示名を捨てない / casing 変更が編集ユーザーに追従する
  - _Requirements: 1.3_

- [x] 3. 読み出し経路を per-user 表示名へ（store read）
  - `TagsByNormalizedNames`: `MIN(it.display_name)` を `items.user_id` でスコープして解決
    （他ユーザー表示名の漏洩を遮断）
  - `ListTagsWithCountFiltered`（サイドバー facet）: 同様に `MIN(it.display_name)` を per-user 解決
  - `ListItems` / `GetItemDetail`: `array_agg(it.display_name ORDER BY t.id)` で per-user 表示名を
    返し、3 配列を `t.id` 整列でアライン
  - _Requirements: 1.3, 1.5, 4.5_

- [x] 4. チップ列の SSR レンダリングと URL 生成（実装済み・本リデザインで不変）
  - `buildActiveTagFilters` / `buildTagRemovedURL` / `buildClearAllTagsURL` と
    `templates/items_list.html` の SSR チップ列。表示名 source が per-user 化された `store.Tag.Name`
    へ切り替わるだけで helper API は不変
  - _Requirements: 1.1, 1.2, 1.4, 2.1, 2.6, 3.1, 3.2, 3.6, 5.1, 5.2, 5.3, 6.4, 6.5_

- [x] 5. クライアント側チップ操作 JS（実装済み・本リデザインで不変）
  - `static/items_active_filters.js`: チップ click / すべてクリア / popstate のフラグメント差し替え、
    AbortController slot 共有、サイドバー・カード上タグとの双方向同期
  - _Requirements: 2.2, 2.3, 2.4, 2.5, 4.1, 4.2, 4.3, 4.4, 6.1, 6.2, 6.3, NFR 1.1, NFR 1.2, NFR 1.3, NFR 2.1, NFR 2.3, NFR 3.1, NFR 3.2_

- [x] 6. マルチテナント分離・conflict 回帰テスト（integration）
  - `TestTagsByNormalizedNamesMultiTenantIsolation`: 2 ユーザーが同一 normalized を異なる表示名で
    持つとき各自の表示名のみを見る
  - `TestTagsByNormalizedNamesAlsoSurfacesViaFacet`: サイドバー facet 経路でも分離を検証
  - `TestCreateAndPatchAgainstExistingSharedRow`: 既存共有行で Create が表示名を捨てない / Patch で
    casing 変更が反映
  - `TestActiveFilterChipsAreMultiTenantIsolated`: server 層 chip 構築 end-to-end で per-user 分離
  - 既存 `TestSaveAndEditPathPreservesDisplayName` / `TestHandleUIItemsFullPageZeroResultResolvesDisplayName`
    が新モデルで green を維持することを確認
  - _Requirements: 1.3, 4.5_

- [x] 7. spec 文書整合（design / tasks）
  - `design.md`（データモデル / Components and Interfaces / Requirements Traceability /
    Migration Strategy / Testing Strategy）と本 `tasks.md` を追加し、codex [medium] の
    requirements ⇄ design ⇄ tasks トレーサビリティ欠落を解消
  - `requirements.md` は既存 AC を尊重（増減なし）
  - _Requirements: 1.3, 4.5_
