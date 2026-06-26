# Implementation Plan

本 spec のタスクは store → server → templates → static JS → CSS の依存順で実装する。
backend / frontend / store / migration を **責務単位**で分割し、1 タスクが `DEV_MAX_TURNS=60`
以内に収まる粒度を意識した（マイグレーションは新規追加不要のため省略）。各タスクは独立 commit
として完結できる。

- [ ] 1. store 層: BulkDeleteItems / BulkAddItemTag / BulkTagResult 追加
  - `internal/store/items_bulk.go` を新規作成:
    - `BulkTagResult` 構造体（`ItemID string` + `Tags []Tag`）を package 公開
    - `BulkDeleteItems(ctx, userID, itemIDs []string) (succeeded []string, err error)` を実装:
      - 単一トランザクション内で `DELETE FROM item_contents WHERE item_id = ANY($1) AND EXISTS
        (SELECT 1 FROM items WHERE id = item_contents.item_id AND user_id = $2)`、`DELETE FROM
        item_tags WHERE item_id = ANY($1) AND EXISTS (...)` を実行
      - `DELETE FROM items WHERE id = ANY($1) AND user_id = $2 RETURNING id` を実行し、
        `pgx.Rows` を `rows.Scan` で `succeeded []string` に貯める
      - 既存 `DeleteItem` と同じ orphan tags 削除を末尾で実行
      - tx 失敗時は全体を rollback し err を返す（succeeded は nil）
    - `BulkAddItemTag(ctx, userID, itemIDs []string, tagInput TagInput) (succeeded []BulkTagResult, err error)` を実装:
      - 単一トランザクション内で以下を順次実行:
        1. **タグ upsert**: `INSERT INTO tags (id, name, normalized_name) VALUES (gen_random_uuid(),
           $tagName, $tagNormalized) ON CONFLICT (normalized_name) DO UPDATE SET normalized_name =
           excluded.normalized_name RETURNING id`（既存行の id を取り出す慣用句、`DO NOTHING` だと
           RETURNING が空になるため `DO UPDATE` で no-op upsert を使う）
        2. **所有確認**: `SELECT id FROM items WHERE id = ANY($1) AND user_id = $2` で
           ownedItemIDs を取得
        3. **item_tags 追加**: `INSERT INTO item_tags (item_id, tag_id, display_name) SELECT
           id, $tagID, $displayName FROM unnest($ownedItemIDs::uuid[]) AS id ON CONFLICT
           (item_id, tag_id) DO NOTHING`（既存のユニーク制約 `(item_id, tag_id)` 前提）
        4. **更新後タグ集合 SELECT**: `SELECT it.item_id, t.id, it.display_name, t.normalized_name
           FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id = ANY($ownedItemIDs)
           ORDER BY it.item_id, t.normalized_name`
      - 結果を ownedItemIDs ごとに `BulkTagResult` に詰めて返す
    - 両関数とも `len(itemIDs) == 0` の早期 return（`return []string{}, nil` / `return []BulkTagResult{}, nil`）
  - 既存 `internal/store/store.go` / `internal/store/tags.go` の修正は **不要**（新規ファイル
    `items_bulk.go` で完結）
  - **テスト追加（同 task 内）**: 本タスクの store 関数は実 DB に依存するため、unit test は
    含めず、次タスク 2 で `//go:build integration` 付きの実 DB テストとして集中検証する。
    本タスクは store 関数の **新規実装** で、ビジネスロジックの直接的な振る舞いは Web/API
    から呼ばれて初めて観察可能なため、`_Requirements:_` に列挙する AC のうち per-item 成功・
    失敗の振る舞い検証はタスク 2 の integration test に **deferred** する
  - _Requirements: 4.4, 4.5, 5.3, 5.4, 8.1, 8.2, 8.3_
  - _Requirements_partial: 4.4, 4.5, 5.3, 5.4, 8.1, 8.2, 8.3_
  - _Boundary: Store_

- [ ] 2. store 層 integration test: 認可・部分失敗・重複防止の実 DB 検証
  - `internal/store/items_bulk_test.go` を新規作成（`//go:build integration` tag、既存
    `items_active_filters_integration_test.go` の `newIntegrationStore` / fixture seed パターンを
    踏襲）:
    - `TestBulkDeleteItems_DeletesOwnAndIgnoresOthers`: user A 3 件 + user B 2 件を seed → user A
      で 5 件 (3 own + 2 other) 全 id を渡して `BulkDeleteItems` 実行 → succeeded は user A の
      3 件のみ、user B の 2 件は DB 上残存（NFR 2.1 leak 防止 / Req 8.1 / 8.2）
    - `TestBulkDeleteItems_PartialFailureFromMissingID`: own 2 件 + 存在しない uuid 3 件を渡す
      → succeeded は own 2 件のみ、エラーなし（Req 4.7 / 4.8 の前提となる per-item 成功・失敗
      の分離を store 層で回帰固定）
    - `TestBulkDeleteItems_DeletesItemTagsAndContents`: items + item_tags + item_contents を
      持つ 2 件を一括削除 → items / item_tags / item_contents の各テーブルから削除済み、orphan
      tags が削除されていることを assert（既存 `DeleteItem` と同じ FK cleanup 規約）
    - `TestBulkDeleteItems_EmptyIDsReturnsEmptySlice`: 空配列で呼び出し → succeeded=[]、err=nil
    - `TestBulkAddItemTag_AddsToOwnedOnlyAndDedupes`: 既に当該タグを持つ item と持たない item を
      混在 → 持たない item にのみ追加、持つ item は重複追加されない（Req 5.4 ON CONFLICT DO NOTHING）、
      user B 所有 item は触らない（Req 8.1）
    - `TestBulkAddItemTag_PreservesExistingTags`: 既存タグ 3 件持つ item + 新規タグ追加 →
      既存タグ全て維持、新規タグが末尾に追加（Req 5.3 / 5.4）
    - `TestBulkAddItemTag_ReturnsFullTagListPerItem`: succeeded[].Tags が更新後の全タグ集合
      （既存 + 新規）を含むことを assert（Req 5.5 の前提）
    - `TestBulkAddItemTag_NewTagCreatesTagsRow`: 既存 tags テーブルに存在しないタグを追加 →
      tags 行が新規作成され、item_tags が紐付くことを assert
    - `TestBulkAddItemTag_PartialFailureFromOtherUserID`: own 2 件 + user B 所有 1 件を渡す →
      succeeded は own 2 件のみ（Req 8.1 / 8.2）
  - 既存 `seedItemsActiveFilterUser` 系の helper パターンを参考に、cleanup（テスト DB を汚さない）
    も同規約に揃える
  - **テスト追加（同 task 内）**: タスク 1 から deferred された Req 4.4 / 4.5 / 5.3 / 5.4 / 8.1 /
    8.2 / 8.3 の store 層検証を本タスクで完結させる
  - _Requirements: 4.4, 4.5, 4.7, 4.8, 5.3, 5.4, 5.5, 8.1, 8.2, 8.3_
  - _Boundary: Store_
  - _Depends: 1_

- [ ] 3. server 層: ハンドラ + ルート + ユニットテスト
  - `internal/server/items_bulk.go` を新規作成:
    - 定数 `maxBulkItemsPerRequest = 100`（NFR 2.1 server enforcement boundary）
    - リクエスト / レスポンス型: `BulkDeleteRequest` / `BulkDeleteResponse` /
      `BulkTagRequest` / `BulkTagResponse` / `BulkTagSuccessDetail` / `BulkFailureDetail`
      （design.md「Components and Interfaces」節の型定義に従う）
    - `handleBulkDeleteItems(w, r)`:
      - `requireAuth` 通過後、`s.limiter.Allow(user.ID)` 検査
      - JSON `{"item_ids": [...]}` を decode、`len(item_ids) == 0` → 400 invalid_request、
        `len(item_ids) > 100` → 400 payload_too_large
      - **UUID 形式の per-id 検証**（design.md Components節 / Security Considerations節）:
        各 `item_ids[i]` を `uuid.Parse(id)` で検証する。invalid な id は store に渡さず、
        その id を `failed[{item_id: <as-is>, reason: "not_found"}]` に **collapse** する。
        valid な id だけを `validIDs []string` に集めて store.BulkDeleteItems に渡す
        （これにより不正文字列を介した DB エラー誘発 / 500 を防ぐ / Req 8.3 二重防御）
      - `s.store.BulkDeleteItems(ctx, user.ID, validIDs)` を呼び、err なら 500 db_error
      - succeeded set を作り `failed := (validIDs \ succeededSet) ∪ invalidUUIDs` を計算
        （invalid UUID 由来の failed と not-found 由来の failed を同じ `reason: "not_found"` で
        合流させる）
      - failed の各 id について `reason: "not_found"`、title / url は空文字（leak 防止）で
        `BulkFailureDetail` を組み立てる
      - `slog.Info("items.bulk.delete", ...)` を出力（user_id / item_ids / succeeded_count /
        failed_count / failed_ids / request_id）
      - 200 で `BulkDeleteResponse` を返す
    - `handleBulkTagItems(w, r)`:
      - 同じ chain 検査
      - JSON `{"item_ids": [...], "tag": "..."}` を decode、`item_ids` 空 / 超過は上と同じ
      - **UUID 形式の per-id 検証**: handleBulkDeleteItems と同じ流儀。invalid な id は store に
        渡さず `failed[{item_id: <as-is>, reason: "not_found"}]` に collapse、`validIDs` だけを
        store に渡す（Req 8.3 二重防御）
      - `tag.Normalize(req.Tag)` 結果が空文字 → 400 invalid_tag（Req 5.9 二重防御）
      - `normalizeTagInputs([]string{req.Tag})[0]` で `TagInput`（Name + NormalizedName）を作る
      - `s.store.BulkAddItemTag(ctx, user.ID, validIDs, tagInput)` を呼ぶ
      - succeeded set / failed を上と同様に計算（invalid UUID 由来の failed を合流）
      - `slog.Info("items.bulk.tag", ...)` を出力
      - 200 で `BulkTagResponse` を返す
  - `internal/server/server.go` の `/v1/items` route 内（`r.Delete("/{id}", ...)` の隣、または
    `r.Patch("/{id}/status", ...)` の隣）に以下 2 行を追加:
    - `r.Post("/bulk-delete", s.requireAuth(s.handleBulkDeleteItems))`
    - `r.Post("/bulk-tag", s.requireAuth(s.handleBulkTagItems))`
  - **既存ハンドラの変更は行わない**（NFR 3.4 既存単一アイテム API の同等提供を維持）:
    `handleDeleteItem` / `handleUpdateItemTags` / `handlePatchItem` / `handleSetItemStatus` /
    `handleListItems` / `handleUIItems` および `extension_contract_test.go` には一切手を入れない
  - `internal/server/items_bulk_test.go` を新規作成（通常 `go test ./...` 経路で実行可能な
    unit test）:
    - `TestHandleBulkDeleteItems_UnauthorizedReturnsJSON401`: requireAuth 未通過 → 401
      `{"error":"unauthorized"}`
    - `TestHandleBulkDeleteItems_InvalidJSONReturns400`: parse 不能 → 400 invalid_request
    - `TestHandleBulkDeleteItems_EmptyIDsReturns400`: `{"item_ids": []}` → 400 invalid_request
    - `TestHandleBulkDeleteItems_OverLimitReturns400PayloadTooLarge`: 101 件 → 400
      payload_too_large（NFR 2.1 server enforcement の回帰固定）
    - `TestHandleBulkTagItems_UnauthorizedReturnsJSON401`: 同上
    - `TestHandleBulkTagItems_InvalidJSONReturns400`: 同上
    - `TestHandleBulkTagItems_EmptyIDsReturns400`: 同上
    - `TestHandleBulkTagItems_OverLimitReturns400PayloadTooLarge`: 同上
    - `TestHandleBulkTagItems_EmptyTagReturns400InvalidTag`: `{"tag": "   "}` または `{}` →
      400 invalid_tag（Req 5.9 server 二重防御）
    - `TestHandleBulkTagItems_NormalizationEmptyTagReturns400InvalidTag`: `{"tag": "　 "}`
      （全角空白等の正規化後空文字パターン） → 400 invalid_tag
    - `TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound`: `{"item_ids":["not-a-uuid", "<valid-uuid>"]}`
      で store には valid な id のみが渡され、invalid な id は `failed[{item_id:"not-a-uuid",
      reason:"not_found"}]` として返ることを fake store を使った handler 層テストで assert
      （Req 8.3 / Security Considerations 節の不正 id 攻撃面遮断の回帰固定）。store interface は
      テスト用に minimal fake で差し替える
    - `TestHandleBulkTagItems_InvalidUUIDsCollapseToFailedNotFound`: 上と同じ流儀で bulk-tag 側も
      回帰固定
    - `TestBulkRoutesRegisteredOnRouter`: chi router の routing tree を walk して
      `POST /v1/items/bulk-delete` / `POST /v1/items/bulk-tag` の 2 route が登録済みであることを
      assert（design.md「Routing Glue」節、chi v5 の `chi.Walk` でツリーを枚挙し path + method を
      照合）。`/{id}` ワイルドカード route と前者の静的セグメントが競合しない（404 にならない）
      ことを併せて確認
  - `extension_contract_test.go` は **変更しない**（既存契約に影響なし / NFR 3.4 / 3.5）
  - **テスト追加（同 task 内）**: 上記 13 件の handler unit テスト（基本 10 件 + UUID 検証 2 件 +
    ルート登録 1 件）を本タスクで完結させる（Req 5.9 / 8.1 / 8.3 / NFR 2.1 / NFR 3.4 の 400 /
    401 / payload_too_large 系 + UUID 形式不正の collapse + 静的ルートと `/{id}` ワイルドカードの
    非競合は通常 `go test ./...` で実行可能 / 同 task 内テスト必須カテゴリに該当）。成功時の
    per-item 部分失敗レスポンス検証は実 DB が必要なため、次タスク 4 の integration test に
    deferred する
  - _Requirements: 4.1, 4.7, 4.8, 5.7, 5.8, 5.9, 8.1, 8.2, 8.3, NFR 2.1, NFR 3.4, NFR 5.1_
  - _Requirements_partial: 4.7, 4.8, 5.7, 5.8, 8.2, NFR 5.1_
  - _Boundary: Server_
  - _Depends: 1_

- [ ] 4. server 層 integration test: 部分失敗レスポンス + 構造化ログの実 DB 検証
  - `internal/server/items_bulk_integration_test.go` を新規作成（`//go:build integration` tag、
    既存 `items_active_filters_integration_test.go` の `newIntegrationServer` / seed パターンを
    踏襲）:
    - `TestHandleBulkDeleteItems_PartialFailureResponse`: 実 DB に own 3 件 + other-user 2 件 +
      存在しない 1 件を seed → POST `/v1/items/bulk-delete` → 200 + succeeded=3 件 + failed=3 件
      （reason: "not_found"）を assert。failed の title / url は空文字（leak 防止 / Req 8.2 / 8.3）
    - `TestHandleBulkDeleteItems_AllSuccessResponse`: own 3 件のみ → succeeded=3、failed=[] /
      slog に `succeeded_count: 3` / `failed_count: 0` が含まれる
    - `TestHandleBulkDeleteItems_LogsStructuredFields`: 成功時の `items.bulk.delete` log line に
      `user_id` / `item_ids` / `succeeded_count` / `failed_count` / `failed_ids` / `request_id`
      の 6 フィールドが含まれ、Cookie / Authorization header / body raw が含まれないことを
      assert（NFR 5.1）。slog handler を test 用 buffer に差し替え
    - `TestHandleBulkTagItems_SucceedsAndReturnsFullTags`: own 2 件 + 既存タグ 1 件持つ
      seed → POST `/v1/items/bulk-tag` `{"item_ids": [...], "tag": "GoLang"}` → 200 +
      succeeded[0].tags に既存 + 新規 `golang` を含むことを assert（Req 5.3 / 5.4 / 5.5）
    - `TestHandleBulkTagItems_PartialFailureFromOtherUserID`: own 2 件 + other-user 1 件 →
      succeeded=2 件、failed=1 件（reason: "not_found"）
    - `TestHandleBulkTagItems_LogsStructuredFields`: NFR 5.1 同様、slog line の field 検査
    - `TestHandleBulkTagItems_DedupesExistingTagInRequest`: 既に当該タグを保持する item を
      含めて呼び出し → 重複なく succeeded に含まれ、レスポンスの tags も重複なし（Req 5.4）
  - 既存 CI（`.github/workflows/ci.yml`）には integration tag 対応が無いため、本タスクの
    テスト群は **stage-a-verify の `go test ./...` には含まれない**（task 2 と同じ運用、verify
    block 末尾の「Integration test の取扱」節を参照）
  - **テスト追加（同 task 内）**: タスク 3 から deferred された Req 4.7 / 4.8 / 5.7 / 5.8 / 8.2 /
    8.3 / NFR 5.1 の API 層成功 / 部分失敗振る舞いを本タスクで完結させる
  - _Requirements: 4.5, 4.7, 4.8, 5.3, 5.4, 5.5, 5.7, 5.8, 8.1, 8.2, 8.3, NFR 5.1_
  - _Boundary: Server_
  - _Depends: 1, 3_

- [ ] 5. SSR テンプレート: items_list のチェックボックス + items.html の選択ツールバー + タグ入力 dialog
  - `templates/items_list.html`:
    - 各 `<article class="tile item-card ...">` に `data-item-id="{{.ID}}"` を追加（既存
      `aria-labelledby` は維持。chi の closest('.item-card') で id を解決する用）
    - `<a class="tile-link" href="...">` の **直前** に以下を挿入:
      ```html
      <input type="checkbox"
             class="item-select"
             data-item-select
             data-item-id="{{.ID}}"
             aria-label="アイテムを選択: {{.Title}}">
      ```
  - `templates/items.html`:
    - `<section class="split">` の終了 (`</section>`) と既存 script 群の間に以下を追加:
      ```html
      <div class="bulk-toolbar" data-bulk-toolbar role="region" aria-label="一括操作" hidden>
        <span class="bulk-toolbar-count"><span data-bulk-count>0</span> / <span data-bulk-limit>100</span> 件選択中</span>
        <div class="bulk-toolbar-actions">
          <button type="button" class="btn-danger bulk-delete">一括削除</button>
          <button type="button" class="btn-secondary bulk-tag">一括タグ付け</button>
          <button type="button" class="btn-tertiary bulk-clear">選択解除</button>
        </div>
      </div>
      <dialog class="bulk-tag-dialog" data-bulk-tag-dialog aria-labelledby="bulk-tag-dialog-title">
        <h2 id="bulk-tag-dialog-title">選択中のアイテムにタグを付与</h2>
        <form method="dialog" data-bulk-tag-form>
          <label class="field">
            <span class="field-label">タグ名</span>
            <input class="input" type="text" data-bulk-tag-input autofocus required>
          </label>
          <div class="dialog-actions">
            <button type="button" class="btn-secondary" data-bulk-tag-cancel>キャンセル</button>
            <button type="submit" class="btn-primary" data-bulk-tag-confirm>付与</button>
          </div>
        </form>
      </dialog>
      ```
    - 既存 `<script src="/static/items_status.js?v={{assetVersion}}" defer></script>` の直後に
      `<script src="/static/items_bulk_selection.js?v={{assetVersion}}" defer></script>` と
      `<script src="/static/items_bulk_actions.js?v={{assetVersion}}" defer></script>` を追加
  - SSR で `hidden` 属性付きでツールバーを描画することで、JS 無効環境では表示されない
    （NFR 3.5: JS 無効環境の閲覧動線維持）
  - **既存テンプレートの構造を維持**（NFR 3.1 / 3.2 / 3.3 / 3.4 後方互換性）:
    - 既存 status-tabs（Issue #119 markup）・active-filters chips（Issue #115 markup）・
      タグ chip ボタン（Issue #117 markup）・既存単一アクション（Mark read / Archive / Refetch /
      Delete / Original）を一切削除・改名・属性変更しない
    - 既存 `<input type="hidden" name="status">` および `<input type="checkbox" name="tag">`
      も触らない（chi v5 form 動作の後方互換）
  - **テスト追加（同 task 内）**: テンプレート差分の単体 Go test は本リポジトリの既存規約に
    倣い追加せず、目視確認 + 次タスク 6 / 7 / 8 の JS / CSS テストで間接的にカバーする（既存
    Issue #115 / #117 / #119 と同じ運用方針）
  - _Requirements: 1.1, 1.5, 3.1, 3.3, 5.1, 6.4, NFR 3.1, NFR 3.2, NFR 3.3, NFR 3.4, NFR 3.5, NFR 4.1, NFR 4.2_
  - _Boundary: Templates_
  - _Depends: 3_

- [ ] 6. static JS: items_bulk_selection.js（選択状態 + Shift範囲 + キーボード + リセット契機）
  - `static/items_bulk_selection.js` を新規作成（既存 `items_active_filters.js` / `items_status.js`
    の IIFE + `init({document, window})` パターン、`vm.createContext` でテスト可能な構造を踏襲）:
    - 内部 `Set<itemID>` で選択状態を保持
    - `[data-items-region]` 上の `change` イベントを delegated 捕捉 → `target.matches('input.item-select')`
      なら toggle 処理（Req 1.1〜1.3）
    - `click` イベントを delegated 捕捉して `e.shiftKey` を見る:
      - shift+click かつ `lastClickedID !== null` なら、現在の DOM 順（`document.querySelectorAll('.item-card')`
        の順）で `lastClickedID` から currentID までの範囲を `Set` に追加（Req 2.1 / 2.2 / 2.3）
      - **shift+click では `e.preventDefault()` を即時に呼び、ブラウザのネイティブ checkbox
        toggle を抑止する**。これは、既に選択済みの終端を Shift+クリックした場合に、ブラウザの
        標準挙動が当該 checkbox を unchecked に戻してしまい、本モジュールの範囲算出結果と
        DOM 状態が乖離するのを防ぐため（Req 2.1「範囲すべてを選択状態」の整合保証）。
        `preventDefault()` 後はモジュール側で当該範囲の checkbox を programmatic に `checked = true`
        へ揃え、`.is-selected` class と `bulkselection:changed` event を同期発火する
      - shift+click でも `lastClickedID === null` なら通常の単一 toggle として扱う（Req 2.4）。
        この経路では `preventDefault()` は呼ばず、change ハンドラの通常 toggle 経路に委ねる
      - 通常 click（shift なし）は change ハンドラに委ねる（preventDefault しない）
      - **`lastClickedID` の更新は通常 click / shift+click のいずれの経路でも実行する**
        （currentID で上書き）。Req 2.3 の「直前に起動された選択操作要素」を、次回の範囲選択
        起点として正しく追従させるため。例: id1 → id5 を Shift 選択した後の Shift+id8 は
        `5→8` の範囲を選択する（`1→8` ではない）
    - `document` 上の `keydown` を捕捉、既存 `app.js` と同じガード（`tag === 'INPUT' / TEXTAREA /
      SELECT / isContentEditable / modifier present` なら return）後、`e.key === 'x'` なら
      `document.activeElement?.closest('.item-card')` の id を toggle（Req 6.1〜6.3）
    - toggle / 範囲選択 / clear / removeFromSelection の **全パス** で:
      - DOM 上の `.item-select[checked]` 同期
      - `<article>` 上の `.is-selected` class 同期（Req 1.4）
      - `data-items-region` に `dispatchEvent(new CustomEvent('bulkselection:changed', {detail: {count, ids}}))`
        を発火（Req 3.6）
    - **上限 100 件 enforcement**（NFR 2.1 / 2.2）:
      - 単一 toggle / shift 範囲の結果として `Set.size > 100` になる場合、超過分は追加せず
        `win.altpocketToast.error('一括操作は最大 100 件までです')` を呼ぶ
      - 既に 100 件選択済みで新規 toggle を試みた場合も同様
    - **fragment 差替リセットと部分失敗時の選択保持の両立**（Req 4.8 / 5.8 / 7.1 / 7.2 / 7.5）:
      `[data-items-region]` 上で `MutationObserver(childList)` を起動し、MutationRecord 受信時に
      以下の **二段階判定** を行ってリセット可否を決める:
      1. **suppression bracket（actions モジュールが actively DOM 削除中か）**:
         内部カウンタ `_actionMutationDepth > 0` のときは reset を行わない。actions 側は
         `selection.beginActionMutation()` で削除前にカウンタを +1、`selection.endActionMutation()`
         で −1 にする（reference counted, nest 安全）。`endActionMutation()` 冒頭で
         `observer.takeRecords()` を呼び出してブラケット中に蓄積した records を破棄してから
         decrement することで、microtask boundary 越しの遅延 callback でも誤発火しない
      2. **fragment 差し替え判定（bracket 外）**:
         - `addedNodes.length > 0` → fragment 差し替え（`innerHTML = newHTML` / `replaceChildren(...)`）
           とみなし `Set.clear()` + `bulkselection:changed` event 発火
         - `addedNodes.length === 0` かつ bracket 外 → 通常運用では発生しないが、保守的に
           reset する（SSR 側が将来空 fragment を返す経路を追加しても Req 7.5 を満たす）
      これにより部分失敗時に actions が succeeded のみを `article.remove()` してもリセットされず
      failed の id が Set に残置される（Req 4.8 / 5.8）。状態タブ切替（Req 7.1）・タグフィルタチップ・
      検索クエリ・ソート・ページ送り変更（Req 7.2）はいずれも `[data-items-region].innerHTML`
      置換に集約されているため `addedNodes.length > 0` で確実にリセットされる
    - **popstate リセット**（Req 7.3 / 7.4）: `win.addEventListener('popstate', () => Set.clear() +
      event 発火)` を register。リロード経路（Req 7.3）は new pageload で Set が空から開始する
      ため追加コード不要だが、確認のため init 時に明示的に `Set` を空に初期化する
    - **既存モジュールへの非干渉**（NFR 3.1 / 3.2 / 3.3）:
      - 既存 `items_status.js`（タブ）・`items_active_filters.js`（チップ）・`items_tags.js`
        （タグクリック）・`items_search.js` の AbortController 共有 slot
        `region.__itemsFragmentInflight` は **読み書きしない**（観察のみ）
      - 既存 `static/app.js` の keyboard handler（`j` / `k` / `o` / `n` / `/` / `?` / `e`）は
        変更せず、本モジュールが独立に `document.addEventListener('keydown')` を register する
      - 既存モジュールの DOM 構造（`.item-card` / `.tile-link` / `.tag-filter-toggle` /
        `.active-filter-chip` / `.status-tab` / `.mark-read-toggle` / `.archive-toggle`）を
        改変しない（チェックボックス 1 個を `<article>` 冒頭に追加するだけ）
    - export 公開 API: `init()` の戻り値として
      `{getSelectedIDs, clear, removeFromSelection, beginActionMutation, endActionMutation}` を
      返す（テストおよび actions モジュールから利用可能）。**`init()` の末尾で同オブジェクトを
      `window.altpocketBulkSelection` にも代入**し、actions 側がスクリプト読み込み順に依存せず
      取得できるようにする（既存 `window.altpocketToast` と同じ流儀、design.md「inter-module API」
      節準拠）
  - `templates/items.html` の script 読み込み行は task 5 で追加済み
  - `static/items_bulk_selection.test.mjs` を新規作成（`node --test`、既存
    `items_status.test.mjs` の fake DOM + vm.createContext パターンを踏襲）:
    - `TestSingleCheckboxToggle`: 1 件 click で Set に追加 + `.is-selected` 付与 +
      `bulkselection:changed` event の detail.count=1（Req 1.1〜1.3 / 1.4）
    - `TestUncheckRemoves`: 同カードを再 click で Set から削除 + class 除去 + count=0
    - `TestShiftClickRange`: 1 件選択済み → 4 件下の Shift+click で間 4 件すべてが選択され、
      合計 5 件 / count=5 になる（Req 2.1）
    - `TestShiftClickPreservesExistingSelection`: 範囲外に既に選択済みの item があれば、その
      選択は保持される（Req 2.2）
    - `TestShiftClickWithoutHistoryActsAsSingleToggle`: `lastClickedID === null` の状態で
      Shift+click → 通常の単一 toggle として扱われる（Req 2.4）
    - `TestShiftClickUpdatesLastClickedAnchor`: 1 件選択 → Shift+5 → さらに Shift+8 →
      範囲は `5→8`（`1→8` ではない）。shift+click 自体が次回の範囲選択起点を更新することを
      回帰固定（Req 2.3）
    - `TestKeyboardXTogglesFocusedCard`: フォーカス中カードで `keydown` `x` → toggle（Req 6.1）
    - `TestKeyboardXIgnoresInputFocus`: `<input>` フォーカス中の `keydown` `x` は no-op（Req 6.3）
    - `TestKeyboardXIgnoresModifierCombo`: `Ctrl+x` / `Meta+x` 等は no-op（既存 app.js 規約 / Req 6.2）
    - `TestUpperLimitRejectsBeyond100`: 100 件選択済み → 101 件目を click で抑止 +
      `toast.error` 呼出 + Set.size は 100 のまま（NFR 2.2）
    - `TestFragmentSwapResetsSelection`: `[data-items-region].innerHTML = ''`（MutationObserver
      を発火させる擬似的差替）→ Set.clear() + event detail.count=0（Req 7.1 / 7.2 / 7.5 を
      同一経路で回帰固定）
    - `TestPopstateResetsSelection`: `win.dispatchEvent(new PopStateEvent('popstate'))` →
      Set.clear() + count=0（Req 7.4）
    - `TestInitialStateIsEmpty`: init 直後の `getSelectedIDs()` が空配列を返す（Req 7.3 リロード時
      の自然な空状態を回帰固定）
    - `TestClearAllProgrammatic`: `init()` 戻り値の `clear()` 呼出 → Set 空 + DOM 上の全
      checkbox が unchecked + 全 `.is-selected` 解除（Req 3.4）
  - **テスト追加（同 task 内）**: 上記 13 件の selection モジュールテストを本タスクで完結させる
    （Req 1.1〜1.4 / 2.1〜2.4 / 3.4 / 3.6 / 6.1〜6.3 / 7.1〜7.5 / NFR 2.2 / NFR 3.1〜3.3 の同 task
    内テスト必須カテゴリに該当）
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.6, 2.1, 2.2, 2.3, 2.4, 3.4, 3.6, 6.1, 6.2, 6.3, 7.1, 7.2, 7.3, 7.4, 7.5, NFR 1.1, NFR 2.1, NFR 2.2, NFR 3.1, NFR 3.2, NFR 3.3_
  - _Boundary: Static_
  - _Depends: 5_

- [ ] 7. static JS: items_bulk_actions.js（ツールバー → 一括削除 / 一括タグ付け → 部分失敗処理）
  - `static/items_bulk_actions.js` を新規作成（既存 `items_status_actions.js` の `init({document,
    window, fetch, toast})` パターン、vm.createContext テスト可能な構造を踏襲）:
    - 起動時に `items_bulk_selection.js` の `init()` が公開する API を取得する経路を確保する。
      実装方針: `window.altpocketBulkSelection` を selection 側で公開し、actions 側がそれを
      参照する（または selection 側が `bulkselection:changed` event の detail で `clear` /
      `removeFromSelection` のコールバックを返却する）
    - **`static/app.js` に `window.altpocketConfirm = confirm` の 1 行を追加**（既存
      `window.altpocketToast = toast` 行の直後に挿入）。既存 module-local な `confirm` ヘルパーを
      bulk actions から再利用するための公開（design.md「Templates」節の方式 1 採用）。
      既存単一アイテム削除動線の挙動には影響しない（`confirm` 関数の参照を 1 つ追加するのみ）
    - `[data-items-region]` の `bulkselection:changed` event を listen し:
      - `count > 0` ならツールバー（`[data-bulk-toolbar]`）の `hidden=false` 化 + `data-bulk-count`
        テキスト更新（Req 3.1 / 3.6）
      - `count === 0` なら `hidden=true` 化（Req 3.2）
    - ツールバーの delegated click を捕捉:
      - `button.bulk-delete` → 既存 `confirm` ダイアログ（`window.altpocketConfirm` 経由、
        無ければ既存 `confirm-overlay` を直接操作）で「N 件を削除しますか？」表示（Req 4.1）→
        approve で `POST /v1/items/bulk-delete`、cancel で何もしない（Req 4.2 / 4.3）
      - `button.bulk-tag` → `<dialog data-bulk-tag-dialog>` を `showModal()`、フォーム submit で
        `<input data-bulk-tag-input>` の値を取得。**空判定のためだけに** `normalizeTagName`
        （`app.js` と同じ NFKC + lowercase）を実行し、正規化結果が空文字なら no-op + input に
        focus 戻す（Req 5.9）。**POST 時は正規化前の原文字列をそのまま送る**（NFKC + lowercase
        を JS 側で強制適用しない / 既存単一アイテム編集では server 側 `normalizeTagInputs` が
        `Name` に原文字列を保持して chip 表示の casing を維持する仕様 / Req 5.2「既存単一
        アイテム編集と同じタグ正規化規則を適用する」）。`POST /v1/items/bulk-tag` の body は
        `{"item_ids": [...], "tag": <原文字列>}`
      - `button.bulk-clear` → `selection.clear()` を呼ぶ（Req 3.4）
    - **一括削除レスポンス処理**:
      - レスポンス型の前提: `BulkDeleteResponse.succeeded` は **`string[]`**（id の配列）、
        `BulkDeleteResponse.failed` は **`BulkFailureDetail[]`**（`{item_id, reason, title?, url?}`）
        （design.md「Components and Interfaces」節の型定義を厳守）。実装では succeeded を
        `string[]` として直接走査し、failed のみ `failed[i].item_id` でアクセスする（succeeded
        側で `.item_id` を読まない）
      - 200 OK + 全成功: succeeded（`string[]`）の各 id について
        `region.querySelector('article[data-item-id="<id>"]')` を fade-out（後述
        「fadeOutAndRemove と beginActionMutation のブラケット規約」参照）で削除、
        `selection.clear()` 後にツールバー隠す（Req 4.4 / 4.5 / 4.6）、`toast.success('N 件削除しました')`
      - 200 OK + 部分失敗: succeeded（`string[]`）の id を DOM 削除 +
        `selection.removeFromSelection(succeeded)`、failed[].item_id は selection 残置（Req 4.8）。
        **failed のタイトル / URL は DOM 削除より先に文字列収集する**（`article[data-item-id="<failed.item_id>"]`
        は actions 側で削除しないため remove 後も DOM 残存だが、収集タイミングが succeeded
        削除より前なら順序依存もなく安全）。`toast.error` で failed 一覧（title または URL を
        含むメッセージ）を表示（Req 4.7）
      - 400 / 401 / 403 / 429 / 500（全件失敗扱い）: **selection 中の各 id について DOM 上の
        article から title / URL を列挙し、`toast.error('N 件の削除に失敗しました: <title 一覧>')`**
        で通知する（Req 4.7「失敗の一部または全部」を満たす）。selection は触らない（残置）。
        ただし toast 文字列は冗長化を避け、選択 5 件以下なら全件列挙、6 件以上なら先頭 3 件 +
        「ほか N 件」形式で省略する
    - **一括タグ付けレスポンス処理**:
      - 200 OK + 全成功: succeeded[].tags を当該カードの `.tags` chip 列に反映する。**反映時は
        必ず `document.createElement` + `textContent` で chip ノードを組み立てる**（タグ名は
        ユーザー入力由来のため `innerHTML` 経由の文字列代入 / `insertAdjacentHTML` は **禁止**、
        保存型 XSS を防ぐ / NFR 5.1 セキュリティ）。既存 chip 列の DOM 子要素は `replaceChildren()`
        または個別 `appendChild` で更新する。`selection.clear()` + dialog 閉鎖 + ツールバー
        隠す（Req 5.5 / 5.6）
      - 200 OK + 部分失敗: succeeded の tags を反映（上と同じ DOM API 経路）+
        `selection.removeFromSelection(succeeded ids)`、failed は selection 残置（Req 5.8）+
        `toast.error` で failed 一覧（Req 5.7、failed タイトル / URL は対象 article の DOM から
        収集 / 一括削除と同じ列挙ルール）
      - 400 invalid_tag: `toast.error('タグ名を入力してください')` + dialog open のまま + 入力欄に focus
      - 4xx / 5xx（全件失敗扱い）: 一括削除と同じく selection 中の article 群から title / URL を
        DOM 収集して `toast.error('N 件のタグ付けに失敗しました: <title 一覧>')` を表示（Req 5.7）。
        selection は触らない
    - **fadeOutAndRemove と beginActionMutation のブラケット規約**（Req 4.8 / 5.8 と既存
      `items_status_actions.js` の fade-out 削除パターンの両立）:
      既存 `items_status_actions.js` の `fadeOutAndRemove` は `setTimeout(remove, 300)` で
      非同期に `article.remove()` を呼ぶ。これを再利用する場合、**`beginActionMutation()` →
      `fadeOutAndRemove()` 起動 → 直後に `endActionMutation()`** という単純なラップでは、
      実 remove() は bracket 閉鎖後に発火し、selection 側 MutationObserver が per-item 削除を
      fragment 差し替えと誤認して Set を空にしてしまう（failed 選択が失われる）。これを防ぐ
      ため、本モジュールは以下のいずれかの方式で fade-out を扱う:
      1. **方式 A（推奨）**: 削除対象 N 件それぞれについて、削除前に
         `selection.beginActionMutation()` を 1 回呼んで bracket カウンタを +1 し、その後
         `setTimeout(() => { article.remove(); selection.endActionMutation(); }, 300)` で
         remove と end を同じ microtask 内で続けて発火させる。reference counted な bracket
         カウンタにより、N 件分の begin/end ペアが全て閉じるまで MutationObserver の reset は
         抑止される
      2. **方式 B**: `fadeOutAndRemove` を再利用せず、synchronous な `article.remove()` を
         beginActionMutation/endActionMutation ブラケット内で発火する（fade-out 視覚効果を
         CSS transition 単体で先行発火させ、`transitionend` event で同期 remove する別経路を
         採る）。本モジュールでは方式 A を採用するため `transitionend` 経路は不要
      タスク 6 で実装する `selection.beginActionMutation` / `endActionMutation` の reference
      counting 仕様（design.md「Selection state」節）に依存するため、方式 A はそのままの依存
      関係で動作する
    - **busy 状態**（NFR 1.2）: click 直後にツールバーに `is-busy` class を付与（CSS task 8 が
      ボタン disabled + spinner を即時表示）。応答完了で外す
    - **NFR 1.3 ちらつき防止**: items-list 全体の innerHTML 書き換えはしない、対象 article のみを
      `article.remove()` する
    - CSRF token は既存 `<meta name="csrf-token">` から取得（`items_status_actions.js` と同じ
      パターン）
    - **キーボード起動の同等挙動**（Req 6.5）: ツールバーボタンはすべてネイティブ `<button>` で
      実装されているため、Tab フォーカス + Enter / Space でクリックと同じ delegated click が
      発火する。本モジュールは modifier なし keydown を別途捕捉しないため、ブラウザ標準の
      キーボード起動経路をそのまま受け入れる
  - `static/items_bulk_actions.test.mjs` を新規作成（`node --test`、`items_status.test.mjs`
    の fake DOM パターン）:
    - `TestDeleteButtonShowsConfirm`: bulk-delete click → confirm ダイアログ表示 + メッセージに
      件数含まれる（Req 4.1）
    - `TestDeleteConfirmCallsAPI`: 承認で fetch が `/v1/items/bulk-delete` を method POST で呼ぶ、
      body に `item_ids` JSON 配列を含む
    - `TestDeleteAllSuccessRemovesCardsAndClearsSelection`: 全成功レスポンス → 該当 article が
      DOM から削除 + selection.clear が呼ばれる（Req 4.5 / 4.6）
    - `TestDeletePartialFailureKeepsFailedSelected`: 部分失敗レスポンス → succeeded の card は
      DOM 削除、failed の id は selection に残置 + toast.error で failed タイトル一覧表示
      （Req 4.7 / 4.8）
    - `TestDeleteCancelDoesNothing`: confirm cancel → fetch 未呼出 / 選択保持（Req 4.3）
    - `TestTagButtonOpensDialog`: bulk-tag click → `<dialog>` open
    - `TestTagDialogEmptyInputIsNoOp`: 空文字 / 全角空白だけ入力 → fetch 未呼出（Req 5.9）+
      input に focus 戻す
    - `TestTagDialogConfirmCallsAPI`: 非空入力 → fetch が `/v1/items/bulk-tag` を呼ぶ、body に
      `item_ids` / `tag` を含む
    - `TestTagSuccessAppliesTagsToCards`: 全成功レスポンス → succeeded[].tags が当該カードの
      `.tags` chip 列に反映 + selection.clear + dialog 閉鎖（Req 5.5 / 5.6）
    - `TestTagPartialFailureKeepsFailedSelected`: 部分失敗 → succeeded の tags 反映 + failed の id
      は selection 残置 + toast.error（Req 5.7 / 5.8）
    - `TestToolbarShowsHidesOnSelectionChange`: `bulkselection:changed` event detail.count=0 → hidden、
      count>0 → 表示 + 件数テキスト更新（Req 3.1 / 3.2 / 3.6）
    - `TestClearButtonCallsSelectionClear`: bulk-clear click → selection.clear が呼ばれる（Req 3.4）
  - **テスト追加（同 task 内）**: 上記 12 件の actions モジュールテストを本タスクで完結させる
    （Req 3.1 / 3.2 / 3.3 / 3.4 / 3.6 / 4.1〜4.8 / 5.5〜5.9 / 6.5 の同 task 内テスト必須カテゴリに該当）
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.6, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 5.1, 5.2, 5.5, 5.6, 5.7, 5.8, 5.9, 6.5, NFR 1.2, NFR 1.3_
  - _Boundary: Static_
  - _Depends: 5, 6_

- [ ] 8. CSS: チェックボックス + 選択カード視覚区別 + 選択ツールバー + タグ入力 dialog
  - `static/style.css` に以下を追加:
    - `.item-select`: カード左上に位置するチェックボックス（`position: absolute; top: var(--space-2);
      left: var(--space-2);` 程度、フォーカスリングは既存 `--focus-ring` トークンに揃える）。
      `.item-card` 側は `position: relative` を確保
    - `.item-card.is-selected`: 選択中の視覚区別（背景を `--bg-selected` 相当 + 縁取りを
      `--border-primary` で強調、ただし既存 `.failed` border-left との衝突を避けるため `outline`
      で表現するか `box-shadow inset` を使う / Req 1.4 視覚区別を色だけに依存しない）
    - `.bulk-toolbar`: `position: sticky; bottom: 0;`（または fixed bottom）でスクロール中も画面下に
      固定（Req 3.5）。背景 `--bg-elevated` + 上端 `--border-default` で本文と区別、`padding`
      は `--space-4`、`hidden` 属性が付いている間は `display: none`
    - `.bulk-toolbar-count`: 件数表示の typography（`--font-size-base` + `--text-secondary`）
    - `.bulk-toolbar-actions`: ボタン 3 個の横並びレイアウト（`display: flex; gap: var(--space-2);`）
    - `.bulk-toolbar.is-busy`: busy 状態（ボタン `pointer-events: none; opacity: 0.65;`、
      アクション群に loading spinner を併置 / NFR 1.2 即時視覚フィードバック）
    - `.bulk-tag-dialog`: ネイティブ `<dialog>` の最低限スタイル（既存 `confirm-overlay` の
      backdrop / shadow / radius トークンに揃える）
    - `.bulk-tag-dialog::backdrop`: dialog 背景 dim（既存 confirm overlay と同じ rgba）
  - light / dark 両テーマで視覚区別が成立することを目視確認（既存トークンを使う限り自動的に
    両テーマで動作する / NFR 4.3 色覚多様性配慮）
  - モバイル（< 768px）でも `bulk-toolbar` がスクリーン下端に貼り付くことを確認（既存
    `filter-toggle-bar` / bottom-sheet と layered 表示にならないか目視）
  - **既存スタイル維持**（NFR 3.1 / 3.2 / 3.3）: `.item-card.failed` の border-left、
    `.item-card[data-status]` の状態スタイル（Issue #119）、`.active-filter-chip`（Issue #115）、
    `.tag-filter-toggle`（Issue #117）の selector・トークンを **削除・改変しない**。
    本タスクは新 selector の **追加のみ** で完結する
  - **テスト追加（同 task 内）**: CSS のみのタスクのため、視覚回帰テストは既存規約上手動目視で
    確認する（既存 #12 / #115 / #117 / #119 と同じ運用）。Go test での追加は不要
  - _Requirements: 1.4, 1.5, 3.5, 5.5, NFR 1.1, NFR 1.3, NFR 4.3_
  - _Boundary: Static_
  - _Depends: 5_

## Verify

本 spec の実装後、watcher（stage-a-verify gate）が再実行すべき verify コマンドを以下の
構造化ブロックで宣言する。Go test と golangci-lint と Node.js 拡張テストの 3 系統を順次実行する。
新規追加する `static/items_bulk_selection.test.mjs`（タスク 6）と `static/items_bulk_actions.test.mjs`
（タスク 7）を node --test 引数に含め、本機能 JS テストがゲートで実行されるようにする。

<!-- stage-a-verify -->
```sh
go test ./... && golangci-lint run && node --test extension/sidepanel.test.mjs static/items_active_filters.test.mjs static/items_search.test.mjs static/items_tags.test.mjs static/items_fragment_race.test.mjs static/items_status.test.mjs static/items_status_tabs.test.mjs static/items_bulk_selection.test.mjs static/items_bulk_actions.test.mjs
```

### Integration test の取扱（stage-a-verify gate スコープ外）

`internal/store/items_bulk_test.go`（タスク 2）と `internal/server/items_bulk_integration_test.go`
（タスク 4）は `//go:build integration` tag 付きで記述するため、上記 `go test ./...` では
**実行されない**（既存 `items_active_filters_integration_test.go` / `store_item_status_test.go`
と同様の運用）。実 PostgreSQL を要するため、watcher 環境では DB を spin-up しない方針
（`.kiro/steering/structure.md` 準拠）。

これらは以下のいずれかで担保する:

- 開発者ローカル: `go test -tags=integration ./internal/store/... ./internal/server/...`
  （`docker compose up -d postgres` で DB を起動した状態で実行）
- Reviewer フェーズ: 必要に応じて Reviewer が同コマンドを手元で実行し AC カバーを確認する
- 既存 CI（`.github/workflows/ci.yml`）には integration tag 対応が無いため本 PR では追加しない
  （integration job 化は別 Issue で扱う方針、Out of Scope）

### per-task Reviewer ループ運用時の deferred test の解消

タスク 1 が `_Requirements_partial:_` で deferred している Req 4.4 / 4.5 / 5.3 / 5.4 / 8.1 / 8.2 /
8.3 の store 層検証は、タスク 2（store integration test）で **解消** する。タスク 3 が
`_Requirements_partial:_` で deferred している Req 4.7 / 4.8 / 5.7 / 5.8 / 8.2 / 8.3 / NFR 5.1
の handler 成功 / 部分失敗 / 構造化ログ検証は、タスク 4（server integration test）で **解消** する。
per-task Reviewer 運用時は、タスク 2 / 4 を「先行 task の deferred test を解消する dedicated
regression test task」として扱う（`.claude/rules/tasks-generation.md` 「task-test 境界整合の規約」
参照）。
