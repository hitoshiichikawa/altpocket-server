# Implementation Plan

本 spec のタスクは store → server → templates → static JS → CSS の依存順で実装する。
backend / frontend / store / migration を **責務単位**で分割し、1 タスクが `DEV_MAX_TURNS=60`
以内に収まる粒度を意識した（マイグレーションは新規追加不要のため省略）。各タスクは独立 commit
として完結できる。

- [x] 1. store 層: BulkDeleteItems / BulkAddItemTag / BulkTagResult 追加
  - `internal/store/items_bulk.go` を新規作成:
    - **重要 / pgx v5 と PostgreSQL UUID 列の型整合**: 本タスクで追加する SQL は `items.id` /
      `item_tags.item_id` / `item_contents.item_id`（いずれも `UUID` 列、`migrations/001_init.sql`
      確認済み）を `ANY($N)` で比較する。pgx v5 は Go `[]string` を `text[]` として encode する
      ため、`uuid_col = ANY($N)` のままでは PostgreSQL の `operator does not exist: uuid = text`
      で実行時エラーになる。**本タスクで追加する全 `ANY($N)` パラメータは `ANY($N::uuid[])`
      明示キャストを必須とする**（既存 `store.go:812` は `text` 列との比較なのでキャスト不要だが、
      本タスクは UUID 列との比較なので必須）
    - `BulkTagResult` 構造体（`ItemID string` + `Tags []Tag`）を package 公開
    - `BulkDeleteItems(ctx, userID, itemIDs []string) (succeeded []string, err error)` を実装:
      - 単一トランザクション内で `DELETE FROM item_contents WHERE item_id = ANY($1::uuid[]) AND
        EXISTS (SELECT 1 FROM items WHERE id = item_contents.item_id AND user_id = $2)`、
        `DELETE FROM item_tags WHERE item_id = ANY($1::uuid[]) AND EXISTS (...)` を実行
      - `DELETE FROM items WHERE id = ANY($1::uuid[]) AND user_id = $2 RETURNING id` を実行し、
        `pgx.Rows` を `rows.Scan` で `succeeded []string` に貯める
      - 既存 `DeleteItem` と同じ orphan tags 削除を末尾で実行
      - tx 失敗時は全体を rollback し err を返す（succeeded は nil）
    - `BulkAddItemTag(ctx, userID, itemIDs []string, tagInput TagInput) (succeeded []BulkTagResult, err error)` を実装:
      - 単一トランザクション内で以下を順次実行（**所有確認を `FOR KEY SHARE` で行い**、0 件のときは
        タグ upsert を実行せず early return することで、認可違反 / 削除済み id のみのリクエストで
        `tags` 行が副作用として作成されず、かつ所有確認後から item_tags INSERT の間に対象 item が
        別 tx で削除されて FK 違反で全体 500 に倒れることを防ぐ / Req 8.2 / 8.3 失敗対象非変更 /
        round 6 review feedback / 元の round 2 review feedback の上位互換）:
        1. **所有確認 + 行ロック取得**: `SELECT id FROM items WHERE id = ANY($1::uuid[]) AND
           user_id = $2 FOR KEY SHARE` で ownedItemIDs を取得。**`FOR KEY SHARE`** は一致した
           `items` 行に対し row-level KEY SHARE ロックを取得し、本 tx commit までの間、当該行への
           並行 `DELETE FROM items WHERE id = <locked-id>`（FOR KEY UPDATE 相当）を **block** する。
           KEY SHARE 同士は互換性があるため、別の並行 `BulkAddItemTag` 呼び出しが同じ id 集合を
           触っても **deadlock せず**、互いに直列化しない。並行 `BulkDeleteItems` のみが本 tx の
           commit を待つ。これにより「step 1 で所有確認 → 別 tx が DELETE → step 4 で
           item_tags.item_id の FK 違反で tx rollback → 500 db_error」の race が完全に閉じる
           （Req 8.3 の「削除済み識別子は変更せず失敗通知」が部分失敗レスポンスとして正しく機能する）
        2. **EARLY RETURN ガード**: `len(ownedItemIDs) == 0` の場合、`return []BulkTagResult{}, tx.Commit(ctx)`
           で即座に return する。**`tags` への INSERT は実行しない**。これにより全 id が
           他ユーザー所有 / 削除済みのリクエストで global `tags` テーブルに新規行が作成され、
           タグサジェスト / chip フィルタに副作用が漏れることを防ぐ
        3. **タグ upsert**（ownedItemIDs が 1 件以上のときのみ実行）: `INSERT INTO tags (id, name,
           normalized_name) VALUES (gen_random_uuid(), $tagName, $tagNormalized) ON CONFLICT
           (normalized_name) DO UPDATE SET normalized_name = excluded.normalized_name RETURNING id`
           （既存行の id を取り出す慣用句、`DO NOTHING` だと RETURNING が空になるため `DO UPDATE`
           で no-op upsert を使う）
        4. **item_tags 追加**: `INSERT INTO item_tags (item_id, tag_id, display_name) SELECT
           id, $tagID, $displayName FROM unnest($ownedItemIDs::uuid[]) AS id ON CONFLICT
           (item_id, tag_id) DO NOTHING`（既存のユニーク制約 `(item_id, tag_id)` 前提）。step 1 の
           FOR KEY SHARE ロックにより、ここで参照する `ownedItemIDs` の全行が `items` に存在することが
           保証されるため、FK 違反は発生しない
        5. **更新後タグ集合 SELECT**: `SELECT it.item_id, t.id, it.display_name, t.normalized_name
           FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id =
           ANY($ownedItemIDs::uuid[]) ORDER BY it.item_id, t.normalized_name`
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
      既存タグ全て維持、新規タグが `succeeded[].Tags` に **含まれる**（store の SQL は `ORDER BY
      it.item_id, t.normalized_name` で normalized 名昇順を返すため、新規タグの位置は normalized 順
      に依存する）。「末尾」と仮定する assertion は書かない（design.md の SQL ORDER BY と整合）
      （Req 5.3 / 5.4）
    - `TestBulkAddItemTag_ReturnsFullTagListPerItem`: succeeded[].Tags が更新後の全タグ集合
      （既存 + 新規）を含むことを assert（Req 5.5 の前提）
    - `TestBulkAddItemTag_NewTagCreatesTagsRow`: 既存 tags テーブルに存在しないタグを追加 →
      tags 行が新規作成され、item_tags が紐付くことを assert
    - `TestBulkAddItemTag_PartialFailureFromOtherUserID`: own 2 件 + user B 所有 1 件を渡す →
      succeeded は own 2 件のみ（Req 8.1 / 8.2）
    - `TestBulkAddItemTag_AllNotOwnedDoesNotCreateTagsRow`: 全 id が他ユーザー所有 / 存在しない
      uuid のみの場合、`tags` テーブルに対象 normalized_name の新規行が作成されない（既存行が
      ある場合はそのまま、無い場合は新規 INSERT もされない）ことを assert。succeeded は空配列
      （Req 8.2 / 8.3 失敗対象非変更 + global `tags` 副作用なし / round 2 review feedback /
      design.md BulkAddItemTag「EARLY RETURN ガード」節と整合）
    - `TestBulkAddItemTag_ConcurrentDeleteBlocksUntilCommit`: own 2 件 (A, B) を seed → goroutine α
      で `BulkAddItemTag(ctx, userID, [A, B], tagInput)` を起動し、tx 内で step 1 の
      `SELECT ... FOR KEY SHARE` 直後を観測できるよう **テスト用 hook（`pgx` 経由で
      `pg_advisory_lock` を使うか、テスト専用の barrier channel を `Store` に inject）** で
      pause させる。goroutine β で同 tx 外から `DELETE FROM items WHERE id = $1`（A 対象）を発火 →
      β が **block している** ことを assert（`pg_locks` を SELECT して `granted=false` の DELETE
      行ロック待ちが存在することで確認、または β の goroutine 完了を timeout 200ms で待って
      非完了を assert）。α の barrier を解除 → α が tx commit → β unblock → β の DELETE が
      完了 → 最終状態は A が削除済み、B はタグ付与済み（item_tags に B のみ存在）。**FOR KEY SHARE を
      付け忘れた古い実装では β が α と並行進行し、α の step 4 INSERT が FK 違反で
      `pgx.ErrTxClosed` 相当を返して 500 db_error になる** / round 6 review feedback の race
      閉鎖の回帰固定 / Req 8.3
  - 既存 `seedItemsActiveFilterUser` 系の helper パターンを参考に、cleanup（テスト DB を汚さない）
    も同規約に揃える
  - **テスト追加（同 task 内）**: タスク 1 から deferred された Req 4.4 / 4.5 / 5.3 / 5.4 / 8.1 /
    8.2 / 8.3 の store 層検証を本タスクで完結させる
  - _Requirements: 4.4, 4.5, 4.7, 4.8, 5.3, 5.4, 5.5, 8.1, 8.2, 8.3_
  - _Boundary: Store_
  - _Depends: 1_

- [ ] 3. server 層: ハンドラ + ルート + ユニットテスト
  - `internal/server/items_bulk.go` を新規作成:
    - 定数: `maxBulkItemsPerRequest = 100`（NFR 2.1 server enforcement boundary）+
      `maxBulkRequestBodyBytes = 16 * 1024`（JSON decode 前のバイト境界 / DoS 面遮断 /
      design.md「Request Size Cap」節）
    - リクエスト / レスポンス型: `BulkDeleteRequest` / `BulkDeleteResponse` /
      `BulkTagRequest` / `BulkTagResponse` / `BulkTagSuccessDetail` / `BulkFailureDetail`
      （design.md「Components and Interfaces」節の型定義に従う）
    - **`bulkItemsStore` interface（test seam / CI 実行 unit test の seam）**: design.md
      「Handler-side store interface」節に従い、`internal/server/items_bulk.go` の冒頭付近に
      package-private な interface を定義する:
      ```go
      type bulkItemsStore interface {
          BulkDeleteItems(ctx context.Context, userID string, itemIDs []string) (succeeded []string, err error)
          BulkAddItemTag(ctx context.Context, userID string, itemIDs []string, tagInput store.TagInput) (succeeded []store.BulkTagResult, err error)
      }
      ```
      `*store.Store` がメソッドシグネチャ一致で自動的に interface を満たすため adapter
      コードは不要
  - `internal/server/server.go` の `Server` struct に `bulkStore bulkItemsStore` フィールドを
    1 つ追加し、`New()` 関数末尾付近で `s.bulkStore = st` を 1 行代入する（既存 `store`
    フィールドは変更せず温存。本 PR の interface 化は **bulk handler 専用**のスコープ最小化 /
    NFR 3.1〜3.4 後方互換）
    - `handleBulkDeleteItems(w, r)`:
      - **拡張機能 / MCP Bearer JWT 遮断**: ハンドラ冒頭（auth context 取り出しの前後問わず、ただし
        return path が共通になる位置で）`if r.Header.Get("Authorization") != "" { writeJSON(403,
        {"error":"forbidden"}); return }`。`requireAuth` は Bearer も session も両受けするため、
        bulk endpoint を **session-only** に絞るには handler 側での明示的 reject が必要
        （requirements.md「Out of Scope: 拡張機能および MCP 経由での一括操作 API 公開」を server で
        固定 / Req 8.1 / 8.2 / 8.3 の goldensource）
      - `requireAuth` 通過後、`s.limiter.Allow(user.ID)` 検査 → false なら 429 rate_limited
      - **request body のバイト境界 enforcement**: JSON decode の前に
        `r.Body = http.MaxBytesReader(w, r.Body, maxBulkRequestBodyBytes)` を 1 行で
        適用する（design.md「Request Size Cap」節 / DoS 面遮断）
      - JSON `{"item_ids": [...]}` を decode。decode エラー時は `errors.As(err, &maxBytesErr)`
        で `*http.MaxBytesError` を判定し、該当なら 400 `{"error":"payload_too_large"}`、
        それ以外（parse 不能 JSON 等）は 400 `{"error":"invalid_request"}`。decode 成功後に
        `len(item_ids) == 0` → 400 invalid_request、`len(item_ids) > 100` → 400 payload_too_large
        （バイト境界を通過した小規模 payload に対する要素数境界 / 二重防御）
      - **UUID 形式の per-id 検証**（design.md Components節 / Security Considerations節）:
        各 `item_ids[i]` を `uuid.Parse(id)` で検証する。invalid な id は store に渡さず、
        その id を `failed[{item_id: <as-is>, reason: "not_found"}]` に **collapse** する。
        valid な id だけを `validIDs []string` に集めて store.BulkDeleteItems に渡す
        （これにより不正文字列を介した DB エラー誘発 / 500 を防ぐ / Req 8.3 二重防御）
      - `s.bulkStore.BulkDeleteItems(ctx, user.ID, validIDs)` を呼び、err なら 500 db_error
        （`s.bulkStore` は `*store.Store` を満たす interface フィールド / design.md
        「Handler-side store interface」節 / test seam）
      - succeeded set を作り `failed := (validIDs \ succeededSet) ∪ invalidUUIDs` を計算
        （invalid UUID 由来の failed と not-found 由来の failed を同じ `reason: "not_found"` で
        合流させる）
      - failed の各 id について `BulkFailureDetail{ItemID, Reason: "not_found"}` を組み立てる
        （**`Title` / `URL` フィールドは struct 自体に存在しない** / leak 防止 / design.md
        Components 節の最終仕様）
      - `slog.Info("items.bulk.delete", ...)` を出力（user_id / item_ids / succeeded_count /
        failed_count / failed_ids / request_id）
      - 200 で `BulkDeleteResponse` を返す
    - `handleBulkTagItems(w, r)`:
      - 同じ chain 検査（Bearer 遮断 + rate limiter + `http.MaxBytesReader` によるバイト境界）
      - JSON `{"item_ids": [...], "tag": "..."}` を decode、decode エラーの
        `*http.MaxBytesError` 判定および `item_ids` 空 / 超過は上と同じ
      - **UUID 形式の per-id 検証**: handleBulkDeleteItems と同じ流儀。invalid な id は store に
        渡さず `failed[{item_id: <as-is>, reason: "not_found"}]` に collapse、`validIDs` だけを
        store に渡す（Req 8.3 二重防御）
      - **`tag` 空判定 → `invalid_tag` 一本化**: `tag.Normalize(req.Tag)` 結果が空文字なら 400
        `{"error":"invalid_tag"}` を返す（Req 5.9 server 二重防御）。`req.Tag == ""` のケースも
        `tag.Normalize("")` で空文字を返す挙動に依存する形で **`invalid_tag` に collapse** し、
        `invalid_request` には混ぜない（クライアント側の invalid_tag 専用処理 / 入力欄 focus 戻しの
        ため categorization を分離する必要がある / design.md Error Categories 節と整合）
      - `normalizeTagInputs([]string{req.Tag})[0]` で `TagInput`（Name + NormalizedName）を作る
      - `s.bulkStore.BulkAddItemTag(ctx, user.ID, validIDs, tagInput)` を呼ぶ
        （`s.bulkStore` は `*store.Store` を満たす interface フィールド / design.md
        「Handler-side store interface」節 / test seam）
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
    - `TestHandleBulkDeleteItems_UnauthorizedReturnsJSON401`: **handler 直接呼び出し（middleware
      バイパス）で auth context 未設定** → 401 `{"error":"unauthorized"}`。既存
      `extension_contract_test.go` の `TestHandleListItemsUnauthorizedReturnsJSONError` と同じ
      pattern（`s.handleBulkDeleteItems(rr, req)` を直接呼ぶ）。実ルート経由では middleware の
      `checkCSRF` が先行するため Authorization 無 + cookie 無の "認証完全に無し" は 403 csrf に
      collapse される（後述 task 4 の integration test で別途検証）
    - `TestHandleBulkDeleteItems_InvalidJSONReturns400`: parse 不能 → 400 invalid_request
    - `TestHandleBulkDeleteItems_EmptyIDsReturns400`: `{"item_ids": []}` → 400 invalid_request
    - `TestHandleBulkDeleteItems_OverLimitReturns400PayloadTooLarge`: 101 件 → 400
      payload_too_large（NFR 2.1 server enforcement の回帰固定）
    - `TestHandleBulkDeleteItems_RejectsBearerAuthReturns403`: `Authorization: Bearer <jwt>` を
      付けて **handler を直接呼び出し**（middleware バイパスで auth context が user 解決済み相当の
      状態にした上で）→ 403 `{"error":"forbidden"}`。Server.store / limiter は呼ばれずに即時
      reject されることを併せて確認（拡張機能 / MCP の bulk endpoint 到達を server で固定 /
      requirements.md Out of Scope の goldensource）。**本テストの対象は「有効 Bearer JWT が
      middleware を通過した後に handler 側で 403 forbidden に拒否される」経路**（design.md
      Architecture Pattern 節「CSRF 保護 / rate limiter / 認証の順序」の 2 段構成）であり、
      **無効 Bearer JWT は本テストの対象外**（middleware の `authenticate` が 401 unauthorized を
      返し handler に到達しないため、`TestHandleBulkDeleteItems_UnauthorizedReturnsJSON401` と
      重複する / round 5 review feedback）
    - `TestHandleBulkDeleteItems_RateLimitedReturns429`: `ratelimit.New(0, 0)` で構成した limiter
      を持つ Server に POST → 429 `{"error":"rate_limited"}`（store は呼ばれない / 既存単一 API
      の rate limit pattern と一致 / 新規 bulk endpoint のレート制御退行の回帰固定）
    - `TestHandleBulkTagItems_UnauthorizedReturnsJSON401`: 同上（handler 直接呼出での auth
      context 未設定 → 401）
    - `TestHandleBulkTagItems_InvalidJSONReturns400`: 同上
    - `TestHandleBulkTagItems_EmptyIDsReturns400`: 同上
    - `TestHandleBulkTagItems_OverLimitReturns400PayloadTooLarge`: 同上
    - `TestHandleBulkTagItems_RejectsBearerAuthReturns403`: 上の bulk-tag 版
    - `TestHandleBulkTagItems_RateLimitedReturns429`: 上の bulk-tag 版
    - `TestHandleBulkTagItems_EmptyTagReturns400InvalidTag`: **valid な `item_ids` を含めて**
      `{"item_ids":["<valid-uuid>"], "tag": "   "}` / `{"item_ids":["<valid-uuid>"], "tag": ""}` /
      `{"item_ids":["<valid-uuid>"]}`（`tag` フィールド欠落）の 3 ケース → いずれも 400
      `{"error":"invalid_tag"}`。`invalid_request` には collapse しないことを assert（Req 5.9
      server 二重防御 / クライアント側 invalid_tag 専用処理の dispatch 契約を固定）。
      **`item_ids` を空にすると先に `invalid_request` で 400 になり tag 検証に到達しない**ため、
      tag 検証経路の回帰固定には valid な id 1 件以上が必須（validation 順序: ①Bearer 遮断
      → ②`s.limiter.Allow` → ③`http.MaxBytesReader` → ④decode + `item_ids` 空/超過
      → ⑤UUID per-id 検証 → ⑥`tag.Normalize` 空判定 / round 2 review feedback）
    - `TestHandleBulkTagItems_NormalizationEmptyTagReturns400InvalidTag`: 同じく valid な `item_ids`
      を含めた `{"item_ids":["<valid-uuid>"], "tag": "　 "}`（全角空白等の正規化後空文字パターン）
      → 400 invalid_tag。`item_ids` 空での invalid_request 先行回避は上記と同じ理由
    - **UUID 形式 collapse / 部分失敗 / 構造化ログを fake store で固定**（round 4 review feedback /
      CI 実行 unit test 経路で認可境界・部分失敗 振る舞いを退行検出する）:
      - `TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound_FakeStore`: handler に
        `bulkStore = &fakeBulkStore{deleteFn: func(ctx, uid, ids) ([]string, error) { return ids, nil }}`
        を注入 → POST `{"item_ids":["not-a-uuid", "<valid-uuid>"]}` → 200 + succeeded に
        valid-uuid のみ含まれ、failed=[{item_id:"not-a-uuid", reason:"not_found"}]。fake の
        deleteFn が呼ばれた際に **invalid uuid が引数に含まれていない** ことを assert（store 層に
        渡らずに handler で collapse される / Req 8.3 / design.md Security Considerations 節）
      - `TestHandleBulkDeleteItems_PartialFailureResponse_FakeStore`: fake の deleteFn が
        `(ids[:2], nil)` を返す（要求 3 件のうち 2 件のみ削除成功）→ 200 + succeeded=2 件、
        failed=1 件（reason: "not_found"）。**レスポンス JSON に `title` / `url` フィールド自体が
        含まれない** ことを assert（`BulkFailureDetail` 構造体から該当フィールドを撤去済み /
        leak 防止 / Req 4.7 / 4.8 / 8.2 / 8.3）
      - `TestHandleBulkDeleteItems_StoreErrorReturns500DBError_FakeStore`: fake の deleteFn が
        `(nil, errors.New("connection lost"))` を返す → 500 `{"error":"db_error"}`（per-item 報告
        なし / 全件選択保持の振る舞いは client 側で確認 / design.md「部分失敗時の atomicity 方針」
        節の DB エラー → 500 db_error 経路）
      - `TestHandleBulkDeleteItems_LogsStructuredFields_FakeStore`: fake で succeeded を返した上で、
        slog handler を test 用 buffer に差し替え、`items.bulk.delete` log line に user_id /
        item_ids / succeeded_count / failed_count / failed_ids / request_id の 6 フィールドが
        含まれ、Cookie / Authorization header / body raw が含まれないことを assert（NFR 5.1）
      - `TestHandleBulkDeleteItems_RequestBodyExceedsByteLimitReturns400PayloadTooLarge`:
        **構文上有効な JSON** で `maxBulkRequestBodyBytes` を超える body を構築して POST →
        400 `{"error":"payload_too_large"}`。**store fake は呼ばれない**（decode が
        `*http.MaxBytesError` を返した時点で reject される / design.md「Request Size Cap」節 /
        DoS 面遮断の回帰固定）。**body 構築規約（round 6 review feedback）**: `bytes.Repeat([]byte("x"), N)`
        のような **JSON として構文不正な byte 列**（先頭文字 `x` が不正トークン）を渡すと、
        `MaxBytesReader` が上限まで読み進める前に `json.Decoder` が syntax error を返し
        `*json.SyntaxError` 経路で `invalid_request` に倒れて本 path の回帰固定にならない。
        代わりに `{"item_ids":["00000000-0000-0000-0000-000000000001", ... (約 500 件複製) ...]}`
        のような **valid UUID を多数並べた配列**（1 UUID あたり約 39 byte なので 500 件で
        ≈ 19.5 KiB が `maxBulkRequestBodyBytes=16 KiB` を確実に超える）を `json.Marshal` で
        構築する。これにより decoder が array element を読み進める途中で `MaxBytesReader` の
        上限を超え、`Decode` が `*http.MaxBytesError` を `errors.Is` 経由で識別可能な形で返す
        （**item_ids 件数 > 100 の検証は decode 完了後**なので、byte 上限到達が先に発火する
        順序が保証される / design.md「Request Size Cap」検証順序節と整合）
      - `TestHandleBulkTagItems_InvalidUUIDsCollapseToFailedNotFound_FakeStore`: 上記の bulk-tag 版
      - `TestHandleBulkTagItems_PartialFailureResponse_FakeStore`: 上記の bulk-tag 版（fake は
        `[]store.BulkTagResult` を返す）
      - `TestHandleBulkTagItems_StoreErrorReturns500DBError_FakeStore`: 上記の bulk-tag 版
      - `TestHandleBulkTagItems_LogsStructuredFields_FakeStore`: 上記の bulk-tag 版（`tag_normalized`
        が含まれることを追加で assert）
      - `TestHandleBulkTagItems_RequestBodyExceedsByteLimitReturns400PayloadTooLarge`: 上記の
        bulk-tag 版。bulk-tag は `tag` field を持つため、body 構築は **valid な item_ids 1 件 +
        巨大な valid `tag` 文字列**で行う: `{"item_ids":["<valid-uuid>"],"tag":"<約 16.5 KiB の
        ASCII 文字列>"}` を `json.Marshal` で構築（`strings.Repeat("a", maxBulkRequestBodyBytes)`
        を `tag` 値に与えて `json.Marshal` させると、JSON 文字列としてエスケープ後の総 body
        サイズが上限を超える）。decoder が `tag` 文字列を読み進める途中で `MaxBytesReader` の
        上限を超え、`*http.MaxBytesError` が `errors.Is` 経由で識別可能な形で返る。bulk-delete 版と
        同じく **store fake は呼ばれない** ことを assert（`bytes.Repeat([]byte("x"), N)` のような
        構文不正な byte 列を渡すと `*json.SyntaxError` 経路に倒れて本 path の回帰固定にならない /
        round 6 review feedback）
      - これら fake-store ベースの unit テストは **通常 `go test ./...` で実行可能**であり、
        既存 CI（`.github/workflows/ci.yml`）の verify gate で退行検出される
        （integration tag 経路に閉じていた task 4 の検証範囲のうち、**handler 層の認可境界 /
        部分失敗 / 構造化ログ振る舞い** を本 task で CI 実行可能な経路に引き上げる）
    - `TestBulkRoutesRegisteredOnRouter`: chi router の routing tree を walk して
      `POST /v1/items/bulk-delete` / `POST /v1/items/bulk-tag` の 2 route が登録済みであることを
      assert（design.md「Routing Glue」節、chi v5 の `chi.Walk` でツリーを枚挙し path + method を
      照合）。`/{id}` ワイルドカード route と前者の静的セグメントが競合しない（404 にならない）
      ことを併せて確認
  - `extension_contract_test.go` は **変更しない**（既存契約に影響なし / NFR 3.4 / 3.5）
  - **テスト追加（同 task 内）**: 上記 25 件の handler unit テスト（Delete 系 11 件
    [unauth / invalid JSON / empty ids / over-limit / bearer reject / rate limit / UUID collapse /
    部分失敗 / store error 500 / 構造化ログ / body bytes 超過] + Tag 系 13 件 [unauth / invalid JSON /
    empty ids / over-limit / bearer reject / rate limit / empty tag invalid_tag / normalize empty
    invalid_tag / UUID collapse / 部分失敗 / store error 500 / 構造化ログ / body bytes 超過] +
    ルート登録 1 件）を本タスクで完結させる。Req 4.7 / 4.8 / 5.7 / 5.8 / 5.9 / 8.1 / 8.2 / 8.3 /
    NFR 2.1 / NFR 3.4 / NFR 5.1 のうち、handler 単体で fake store 経由で観測可能な認可境界 /
    部分失敗 / 構造化ログ振る舞いは本 task で完結し、CI 実行可能な `go test ./...` 経路に乗る。
    実 SQL 経路（store 層の UPDATE/DELETE/INSERT が WHERE user_id を正しく適用するか）の検証は
    task 2（store integration test）と task 4（server integration test）で実 DB を介して別途
    固定する（store 実装の SQL 退行検出は integration が必須のため）
  - _Requirements: 4.1, 4.7, 4.8, 5.7, 5.8, 5.9, 8.1, 8.2, 8.3, NFR 2.1, NFR 3.4, NFR 5.1_
  - _Boundary: Server_
  - _Depends: 1_

- [ ] 4. server 層 integration test: 部分失敗レスポンス + 構造化ログの実 DB 検証
  - `internal/server/items_bulk_integration_test.go` を新規作成（`//go:build integration` tag、
    既存 `items_active_filters_integration_test.go` の `newIntegrationServer` / seed パターンを
    踏襲）:
    - `TestHandleBulkDeleteItems_PartialFailureResponse`: 実 DB に own 3 件 + other-user 2 件 +
      存在しない 1 件を seed → POST `/v1/items/bulk-delete` → 200 + succeeded=3 件 + failed=3 件
      （reason: "not_found"）を assert。**レスポンス JSON に `title` / `url` フィールド自体が
      含まれない** ことを assert（`BulkFailureDetail` 構造体から該当フィールドを撤去済み /
      leak 防止 / Req 8.2 / 8.3）
    - `TestHandleBulkDeleteItems_InvalidUUIDsCollapseToFailedNotFound`: 実 DB に own 1 件 seed
      → POST `{"item_ids":["not-a-uuid", "<valid-own-uuid>"]}` → 200 + succeeded=[valid-uuid]
      + failed=[{item_id:"not-a-uuid", reason:"not_found"}]。500 にならず handler 層で collapse
      されることを assert（Req 8.3 / Security Considerations 節の DB エラー誘発攻撃面遮断の
      回帰固定 / task 3 から移管）
    - `TestHandleBulkTagItems_InvalidUUIDsCollapseToFailedNotFound`: 上の bulk-tag 版。
      `{"item_ids":["not-a-uuid", "<valid-own-uuid>"], "tag": "GoLang"}` → 200 + succeeded
      に valid-uuid のみ含まれ、failed に `{item_id:"not-a-uuid", reason:"not_found"}` が
      含まれることを assert
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
    - `TestBulkRoutesOnRealRouterReturnCSRFForbiddenWithoutAuth`: chi router 経由で
      `POST /v1/items/bulk-delete` / `POST /v1/items/bulk-tag` を **Authorization header 無 +
      `altpocket_session` cookie 無 + `X-CSRF-Token` 無** の状態で呼び出し → 両 endpoint とも
      **403 `{"error":"csrf"}`** を返すことを assert（`requireAuth` の `checkCSRF` が `authenticate`
      より先に走り、Authorization 無の場合に session cookie 不在を csrf エラーで弾く既存挙動の
      回帰固定 / round 2 review feedback）。401 unauthorized は本経路では到達しないため
      assert しない（401 は handler 単体 / task 3 でカバー済み）
  - 既存 CI（`.github/workflows/ci.yml`）には integration tag 対応が無いため、本タスクの
    テスト群は **stage-a-verify の `go test ./...` には含まれない**（task 2 と同じ運用、verify
    block 末尾の「Integration test の取扱」節を参照）
  - **テスト追加（同 task 内）**: task 3 で fake-store 経由の handler 振る舞い
    （UUID collapse / 部分失敗レスポンス / 構造化ログ / DB エラー 500）は CI 実行可能な
    unit test 経路に乗ったが、**本 task では実 DB を介した SQL 経路の検証**（store の
    `BulkDeleteItems` / `BulkAddItemTag` が WHERE user_id 条件を正しく適用するか、
    UPDATE/DELETE/INSERT の RETURNING が認可境界を leak しないか）を完結させる。
    task 3 の fake store では「store が succeeded を正しく返す」前提を仮定しており、
    その前提の SQL 退行検出は integration が必須
  - _Requirements: 4.5, 4.7, 4.8, 5.3, 5.4, 5.5, 5.7, 5.8, 8.1, 8.2, 8.3, NFR 5.1_
  - _Boundary: Server_
  - _Depends: 1, 3_

- [ ] 5. SSR テンプレート: items_list のチェックボックス + items.html の選択ツールバー + タグ入力 dialog
  - `templates/items_list.html`:
    - 各 `<article class="tile item-card ...">` に **`data-item-id="{{.ID}}"` と
      `data-original-url="{{.URL}}"` の 2 属性** を追加（既存 `aria-labelledby` は維持）。
      `data-item-id` は selection / actions モジュールから `closest('.item-card')` で id を
      解決する用、`data-original-url` は **失敗通知時のタイトル空 fallback URL** として
      `article.dataset.originalUrl` で参照する用（既存の `<a class="tile-link" href="/ui/items/<id>">`
      は内部詳細ページ URL であり元記事 URL ではないため URL fallback には使えない /
      Req 4.7 / 5.7 をタイトル空 item でも満たす / design.md Components 節「失敗 toast の表示文言」
      および Security Considerations「PII リーク防止」と整合）
    - `<a class="tile-link" href="...">` の **直前** に以下を挿入（**`disabled` 属性付きで SSR
      する点に注意** / NFR 3.5 / design.md Progressive Enhancement 規約）:
      ```html
      <input type="checkbox"
             class="item-select"
             data-item-select
             data-item-id="{{.ID}}"
             aria-label="アイテムを選択: {{.Title}}"
             disabled>
      ```
      `disabled` で SSR することにより、JS 無効ブラウザでは checkbox が Tab フォーカスを取らず
      クリックも無効化される（ブラウザネイティブ disabled 挙動）。これにより本機能導入前と
      同等の閲覧 / 単一アイテム操作動線が JS 無効環境で維持される（Req NFR 3.5）。
      `items_bulk_selection.js`（task 6）の `init()` が走った時点で `removeAttribute('disabled')`
      され、操作可能になる Progressive Enhancement 規約
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
      <!-- NOTE: `method="dialog"` の submit はブラウザのネイティブ挙動として dialog を
           即座に close する。invalid_tag（Req 5.9）時に dialog を開いたまま input に
           focus 戻しが必要なため、task 7 の actions モジュールの submit ハンドラ冒頭で
           **必ず `event.preventDefault()` を呼び**、close 判定を JS 側に委ねる（task 7 の
           「bulk-tag dialog submit 規約」節 / round 4 review feedback）。 -->
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
      <dialog class="bulk-failure-dialog"
              data-bulk-failure-dialog
              role="alertdialog"
              aria-labelledby="bulk-failure-title"
              aria-describedby="bulk-failure-list">
        <h2 id="bulk-failure-title" data-bulk-failure-title>失敗した項目</h2>
        <ul id="bulk-failure-list" class="bulk-failure-list" data-bulk-failure-list role="list"></ul>
        <div class="dialog-actions">
          <button type="button" class="btn-primary" data-bulk-failure-close>OK</button>
        </div>
      </dialog>
      ```
    - `bulk-failure-dialog` は Req 4.7 / 5.7「失敗したアイテムをユーザーが特定可能な形（タイトルまたは
      URL を含むメッセージ）で通知」を 100 件まで全件 reachable に満たすための SSR 領域。actions
      モジュール（task 7）が `<li>` を populate して `showModal()` する
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
    - **Progressive Enhancement enable**（NFR 3.5 / fragment 差替も追随 / design.md
      「Responsibilities & Constraints」節と整合）:
      - **init 時**: `init()` 起動直後、`region.querySelectorAll('input.item-select[disabled]')`
        を全件走査し `removeAttribute('disabled')` する。これにより SSR で `disabled` 付き出力された
        checkbox は本モジュールが load された場合のみ操作可能になる。本処理は change / click /
        keydown ハンドラを register する **前**に実行する
      - **fragment 差替時**: 後述する MutationObserver の reset callback（`addedNodes.length > 0`
        検出時）の中で、reset と同じタイミングで `region.querySelectorAll('input.item-select[disabled]')`
        を再走査し `removeAttribute('disabled')` を実行する。これを行わないと、SSR が `disabled`
        付きで出力した新しい checkbox が、状態タブ切替・タグフィルタチップ・検索クエリ・ソート・
        ページ送り・popstate 後の fragment swap 経路で操作不能のまま残置され、JS 有効環境でも
        選択操作ができなくなる（Req 1.1 違反 / NFR 3.5 規約の連続性 / round 2 review feedback）。
        既存単一アクション（Mark read / Archive 等）は同 fragment 経路で既に動作しているため、
        新規 checkbox についても同じ操作可能性を担保する必要がある
      - **共通 helper として実装する**: 上記 2 経路は同じロジックなので、本モジュール内に
        `enableSelectionCheckboxes(region)` 関数を 1 つ定義し、init 時と MutationObserver reset
        callback 内の双方から呼び出す（重複実装を避ける）
    - 内部 `Set<itemID>` で選択状態を保持
    - `[data-items-region]` 上の `change` イベントを delegated 捕捉 → `target.matches('input.item-select')`
      なら toggle 処理（Req 1.1〜1.3）
    - `click` イベントを delegated 捕捉して `e.shiftKey` を見る:
      - **shift+click で範囲選択を発動する条件は次の 3 条件すべてを満たすこと**
        （Req 2.1「少なくとも 1 件選択済み」と整合 / round 2 review feedback）:
        1. `selectionSet.size > 0`（少なくとも 1 件選択済み / Req 2.1）
        2. `lastClickedID !== null`（履歴 anchor が存在 / Req 2.3 / 2.4）
        3. `region.querySelector('article[data-item-id="<lastClickedID>"]') !== null`（anchor 要素が
           現在の DOM に存在 / stale anchor 排除）
      - 3 条件すべて満たす shift+click の場合、現在の DOM 順
        （`document.querySelectorAll('.item-card')` の順）で `lastClickedID` から currentID までの
        範囲を `Set` に追加（Req 2.1 / 2.2 / 2.3）
      - **shift+click では `e.preventDefault()` を即時に呼び、ブラウザのネイティブ checkbox
        toggle を抑止する**。これは、既に選択済みの終端を Shift+クリックした場合に、ブラウザの
        標準挙動が当該 checkbox を unchecked に戻してしまい、本モジュールの範囲算出結果と
        DOM 状態が乖離するのを防ぐため（Req 2.1「範囲すべてを選択状態」の整合保証）。
        `preventDefault()` 後はモジュール側で当該範囲の checkbox を programmatic に `checked = true`
        へ揃え、`.is-selected` class と `bulkselection:changed` event を同期発火する
      - shift+click でも上記 3 条件のいずれかが満たされない（選択 0 件 / lastClickedID が null /
        anchor 要素が DOM に不在）場合、通常の単一 toggle として扱う（Req 2.4 fallback）。
        この経路では `preventDefault()` は呼ばず、change ハンドラの通常 toggle 経路に委ねる
      - 通常 click（shift なし）は change ハンドラに委ねる（preventDefault しない）
      - **`lastClickedID` の更新は通常 click / shift+click のいずれの経路でも実行する**
        （currentID で上書き）。Req 2.3 の「直前に起動された選択操作要素」を、次回の範囲選択
        起点として正しく追従させるため。例: id1 → id5 を Shift 選択した後の Shift+id8 は
        `5→8` の範囲を選択する（`1→8` ではない）
    - `document` 上の `keydown` を捕捉、以下のガードを適用してから `e.key === 'x'` を判定する
      （Req 6.1〜6.3）:
      - **modifier present（`ctrlKey` / `altKey` / `metaKey` のいずれか）なら return**（既存
        `app.js` keyboard handler と同じ pattern / Req 6.2 衝突回避）
      - `e.target.tagName === 'TEXTAREA' || e.target.tagName === 'SELECT' ||
        e.target.isContentEditable` なら return（**文字入力フォーカス中の抑止** / Req 6.3）
      - **`e.target.tagName === 'INPUT'` の場合は input の `type` で分岐**:
        - `e.target.matches('input.item-select')` または `e.target.type === 'checkbox' ||
          e.target.type === 'radio' || e.target.type === 'button' || e.target.type === 'submit'`
          のときは **通過させる**（チェックボックスや radio・button 系 input は文字入力では
          ないため、Req 6.1 のキーボードトグルが満たせる必要がある）
        - それ以外（`type === 'text' / 'search' / 'email' / 'url' / 'tel' / 'password' /
          'number' / 'date' / 'time' / 'datetime-local' / 'month' / 'week' / 'color' /
          'file'` 等の文字入力 input、bulk-tag dialog の `data-bulk-tag-input` や検索ボックスを
          含む）は return（Req 6.3 文字入力フォーカス中の抑止）
      - ガード通過後、`e.key === 'x'` なら `document.activeElement?.closest('.item-card')` の
        id を toggle する。フォーカスが `input.item-select` 上にあるケース（Tab ナビゲーション
        などで checkbox にフォーカスがある状態）でも、closest('.item-card') により対応する
        `<article>` の id が解決される（Req 6.1 / round 5 review feedback）
      - **既存 `app.js` keyboard handler との関係**: 本モジュールは独立に
        `document.addEventListener('keydown')` を register する。既存 `app.js` の guard が
        `tag === 'INPUT'` を一律 return する規約は `j` / `k` / `o` / `n` / `/` / `?` / `e` に
        対する判定であり、`x` キーの判定は本モジュール側で上記の精緻化されたガードを適用する
        （既存ハンドラには `x` 分岐が無いため衝突しない / Req 6.2）
    - toggle / 範囲選択 / clear / removeFromSelection の **全パス** で:
      - DOM 上の `.item-select[checked]` 同期
      - `<article>` 上の `.is-selected` class 同期（Req 1.4）
      - `data-items-region` に `dispatchEvent(new CustomEvent('bulkselection:changed', {detail: {count, ids}}))`
        を発火（Req 3.6）
    - **上限 100 件 enforcement**（NFR 2.1 / 2.2 / Req 2.1 の "範囲のカードすべてを選択状態" の
      整合保証）:
      - **単一 toggle**: 既に 100 件選択済みで 101 件目を click / `x` toggle で追加しようとした
        場合、追加せず `win.altpocketToast.error('一括操作は最大 100 件までです')` を呼ぶ
        （101 件目だけが弾かれる）
      - **Shift 範囲選択**: 範囲算出結果と現在の Set の合算が `> 100` になる場合、**範囲全体を
        未選択のまま reject** し（部分追加は行わない）、`win.altpocketToast.error('範囲選択により
        上限を超えるため処理されませんでした')` を呼ぶ。Req 2.1 が「範囲のカードすべてを選択
        状態にする」と "all or nothing" を要求しているため、上限超過時に範囲の一部だけを
        選択する曖昧な状態を生まない（design.md NFR 2.1 / 2.2 Requirements Traceability 行と
        整合）
    - **fragment 差替リセットと部分失敗時の選択保持の両立**（Req 4.8 / 5.8 / 7.1 / 7.2 / 7.5 /
      round 2 review feedback）:
      `[data-items-region]` 上で `MutationObserver(childList)` を起動し、MutationRecord 受信時に
      以下の **per-record 判定** を行う（bracket は per-item 削除 record のみを抑止し、fragment
      差替 record は bracket の有無に関わらず常に reset を発火する。300ms fade-out bracket 中に
      タブ切替 / 検索 / ソート / ページ送りが入っても Req 7.1 / 7.2 / 7.5 のリセットが脱落しない
      ようにするため）:
      1. **`addedNodes.length > 0` の record（fragment 差替）**: bracket カウンタ
         (`_actionMutationDepth`) の状態に **関係なく** reset を実行する:
         - `Set.clear()` + `lastClickedID = null` + `bulkselection:changed` event 発火
           （`lastClickedID` も `null` リセットすることで stale anchor を起点とする後続
           Shift+click 範囲選択を防止 / Req 2.1「少なくとも 1 件選択済み」整合 / round 2 review）
         - 新しい SSR markup 内の `disabled` 付き `input.item-select` を全件 `enableSelectionCheckboxes(region)`
           で enable（NFR 3.5 連続性、上記 Progressive Enhancement 節と同じ helper を呼ぶ）
         - これにより 300ms fade-out bracket 中であっても、状態タブ切替・タグフィルタチップ・
           検索クエリ・ソート・ページ送りで `[data-items-region].innerHTML` が新 SSR に置換された
           瞬間に確実にリセットされる（Req 7.1 / 7.2 / 7.5 の取りこぼし防止）
      2. **`addedNodes.length === 0` の record（per-item 削除）**:
         - `_actionMutationDepth > 0`（actions モジュールが actively `article.remove()` 中）の
           間は **無視する**（reset を行わない / Req 4.8 / 5.8 failed 選択保持）。actions 側は
           `selection.beginActionMutation()` で削除前にカウンタを +1、
           `selection.endActionMutation()` で −1 にする（reference counted, nest 安全）。
           `endActionMutation()` 冒頭で `observer.takeRecords()` を呼び出し、**取り出した
           records は per-record 判定（上記 1 / 2）を通してから discard する**（取り出した
           records を一律に黙って捨てると、bracket 中に貯まった fragment 差替 record の
           Req 7.1 / 7.2 / 7.5 リセットが失われる / round 4 review feedback）。具体的な順序は
           「`takeRecords()` → fragment 差替 record（`addedNodes.length > 0`）を per-record
           判定で処理して reset 実行 → per-item 削除 record（`addedNodes.length === 0`）は
           bracket 中なので discard → decrement」。これにより microtask boundary 越しの遅延
           callback でも誤発火せず、かつ fragment 差替を取りこぼさない
         - `_actionMutationDepth === 0`（bracket 外）→ 通常運用では発生しないが、保守的に
           reset を実行（`Set.clear()` + `lastClickedID = null` + event 発火）。SSR 側が将来空
           fragment を返す経路を追加しても Req 7.5 を満たす
      これにより部分失敗時に actions が succeeded のみを `article.remove()` してもリセットされず
      failed の id が Set に残置される（Req 4.8 / 5.8）。状態タブ切替（Req 7.1）・タグフィルタチップ・
      検索クエリ・ソート・ページ送り変更（Req 7.2）はいずれも `[data-items-region].innerHTML`
      置換に集約されているため `addedNodes.length > 0` で確実にリセットされる
    - **anchor の stale 防止**: 上記の reset 経路（fragment 差替 / popstate / `clear()` 呼出）
      では Set クリアと同時に `lastClickedID = null` を実行する。**さらに `beginActionMutation()`
      ブラケット内で per-item `article.remove()` された article の id が `lastClickedID` と
      一致する場合も、`lastClickedID = null` にリセットする**（actions モジュールが削除前 /
      削除後にどちらで selection.removeFromSelection を呼んでも、anchor の stale 化は selection
      モジュール内で吸収する）。これにより、後続の Shift+click で DOM に存在しない anchor を
      起点として範囲算出するケースを排除する（round 2 review feedback）
    - **popstate リセット**（Req 7.3 / 7.4）: `win.addEventListener('popstate', () => { Set.clear();
      lastClickedID = null; event 発火 })` を register。リロード経路（Req 7.3）は new pageload で
      Set が空から開始するため追加コード不要だが、確認のため init 時に明示的に `Set` を空に
      初期化し、`lastClickedID = null` も初期化する
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
    - `TestShiftClickWithEmptySelectionActsAsSingleToggle`: `lastClickedID !== null` でも
      `selectionSet.size === 0`（直前に選択していたものを全 `clear` した直後等）の状態で
      Shift+click → 単一 toggle に降格（Req 2.1「少なくとも 1 件選択済み」と整合 / round 2
      review feedback / 3 条件 fallback の回帰固定）
    - `TestShiftClickWithStaleAnchorActsAsSingleToggle`: 1 件選択 → 当該 article を fragment
      差替で除去（`region.querySelector('article[data-item-id="<lastClickedID>"]') === null`
      となる）→ さらに別カードを Shift+click → 単一 toggle に降格（stale anchor 起点の範囲算出
      が走らない / round 2 review feedback）。fragment 差替で reset が走るパス（`lastClickedID`
      も `null` 化）と、何らかの理由で reset が走らないが anchor article だけ消えるパスの両方を
      網羅するため、`lastClickedID` を明示的に保持した状態で article のみ DOM から取り除く擬似
      シナリオで再現する
    - `TestShiftClickUpdatesLastClickedAnchor`: 1 件選択 → Shift+5 → さらに Shift+8 →
      範囲は `5→8`（`1→8` ではない）。shift+click 自体が次回の範囲選択起点を更新することを
      回帰固定（Req 2.3）
    - `TestKeyboardXTogglesFocusedCard`: フォーカス中カードで `keydown` `x` → toggle（Req 6.1）
    - `TestKeyboardXIgnoresInputFocus`: `<input>` フォーカス中の `keydown` `x` は no-op（Req 6.3）
    - `TestKeyboardXIgnoresModifierCombo`: `Ctrl+x` / `Meta+x` 等は no-op（既存 app.js 規約 / Req 6.2）
    - `TestUpperLimitRejectsBeyond100`: 100 件選択済み → 101 件目を **単一 click** で抑止 +
      `toast.error('一括操作は最大 100 件までです')` 呼出 + Set.size は 100 のまま（NFR 2.2 単一 toggle 経路）
    - `TestShiftRangeAcrossUpperLimitRejectsEntireRange`: 既に 80 件選択済み → 残り未選択
      カードのうち、Shift+click で結果として 100 件超 (例: 21 件分の範囲) が選択される操作 →
      **範囲全体を未選択のまま reject** + `toast.error('範囲選択により上限を超えるため処理
      されませんでした')` + Set.size は 80 のまま（NFR 2.2 Shift 範囲経路 / Req 2.1 "all or
      nothing" / round 2 review feedback）
    - `TestProgressiveEnhancementRemovesDisabled`: init() 実行直後、SSR で `disabled` 属性付き
      の `input.item-select` が **すべて enabled** になることを assert（NFR 3.5 Progressive
      Enhancement 規約 / round 2 review feedback）
    - `TestFragmentSwapReEnablesNewDisabledCheckboxes`: init 完了後、`[data-items-region]` の
      `innerHTML` を新しい SSR markup（`<article>` 内に `disabled` 属性付き `input.item-select`
      を含む）で差し替える（MutationObserver が fragment 差替を検出する経路）→ reset callback
      内で `enableSelectionCheckboxes` が再実行され、新しい checkbox が **すべて enabled** に
      なることを assert（NFR 3.5 連続性 / 状態タブ切替・タグフィルタ・検索・ソート・ページ送り
      経路の Req 1.1 違反防止 / round 2 review feedback）
    - `TestFragmentSwapResetsSelection`: `[data-items-region].innerHTML = ''`（MutationObserver
      を発火させる擬似的差替）→ Set.clear() + event detail.count=0（Req 7.1 / 7.2 / 7.5 を
      同一経路で回帰固定）+ **`lastClickedID` が `null` にリセットされる**（getLastClickedID
      内部状態の test-only getter または直後の shift+click が単一 toggle に降格することで間接的
      に observe / round 2 review feedback）
    - `TestPopstateResetsSelection`: `win.dispatchEvent(new PopStateEvent('popstate'))` →
      Set.clear() + count=0（Req 7.4）+ `lastClickedID` リセット（round 2 review feedback）
    - `TestFragmentSwapDuringActionBracketStillResets`: actions モジュール挙動を擬似的に再現
      （`beginActionMutation()` 1 回呼出で bracket カウンタ +1 → bracket 解放前に
      `[data-items-region].innerHTML` を新 SSR markup に置き換える） → fragment 差替 record
      の `addedNodes.length > 0` 判定で bracket 状態に関係なく `Set.clear()` + `lastClickedID = null`
      + event 発火 + 新規 disabled checkbox の enable が走ることを assert（300ms fade-out
      bracket 中の状態タブ切替・タグフィルタ・検索・ソート・ページ送りで Req 7.1 / 7.2 / 7.5 の
      リセットが脱落しないことの回帰固定 / round 2 review feedback）
    - `TestEndActionMutationProcessesQueuedFragmentSwapBeforeDiscard`: fragment 差替 record が
      MutationObserver の **pending queue** に貯まっている（callback がまだ発火していない）状態
      で `endActionMutation()` が呼ばれるパスを擬似的に再現する。bracket 中に
      `region.innerHTML = newSSR` を実行 → MutationObserver callback が microtask boundary 越しで
      未発火のまま `endActionMutation()` を呼ぶ → `endActionMutation()` 冒頭の `takeRecords()` が
      fragment 差替 record を取り出し、per-record 判定で `Set.clear()` + `lastClickedID = null` +
      event 発火 + 新 checkbox の enable が走ることを assert。**取り出した records を黙って
      捨てると Req 7.1 / 7.2 / 7.5 のリセットが失われる** ので、その分岐を明示的に固定する
      （round 4 review feedback）
    - `TestInitialStateIsEmpty`: init 直後の `getSelectedIDs()` が空配列を返す（Req 7.3 リロード時
      の自然な空状態を回帰固定）
    - `TestClearAllProgrammatic`: `init()` 戻り値の `clear()` 呼出 → Set 空 + DOM 上の全
      checkbox が unchecked + 全 `.is-selected` 解除（Req 3.4）
  - **テスト追加（同 task 内）**: 上記 21 件の selection モジュールテストを本タスクで完結させる
    （Req 1.1〜1.4 / 2.1〜2.4 / 3.4 / 3.6 / 6.1〜6.3 / 7.1〜7.5 / NFR 2.2 / NFR 3.1〜3.3 / NFR 3.5
    の同 task 内テスト必須カテゴリに該当、`takeRecords()` 経由の queued fragment 差替 record
    処理の回帰固定を含む）
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.6, 2.1, 2.2, 2.3, 2.4, 3.4, 3.6, 6.1, 6.2, 6.3, 7.1, 7.2, 7.3, 7.4, 7.5, NFR 1.1, NFR 2.1, NFR 2.2, NFR 3.1, NFR 3.2, NFR 3.3, NFR 3.5_
  - _Boundary: Static_
  - _Depends: 5_

- [ ] 7. static JS: items_bulk_actions.js（ツールバー → 一括削除 / 一括タグ付け → 部分失敗処理）
  - `static/items_bulk_actions.js` を新規作成（既存 `items_status_actions.js` の `init({document,
    window, fetch, toast})` パターン、vm.createContext テスト可能な構造を踏襲）:
    - 起動時に `items_bulk_selection.js` の `init()` が公開する API を取得する経路を確保する。
      実装方針: `window.altpocketBulkSelection` を selection 側で公開し、actions 側がそれを
      参照する（または selection 側が `bulkselection:changed` event の detail で `clear` /
      `removeFromSelection` のコールバックを返却する）
    - **`static/app.js` への 2 つの公開行追加**（design.md「Templates」節の最終規約）:
      - `window.altpocketConfirm = confirm;` を `const confirm = (() => { ... })();` の **直後
        の行**（現状 line 120 付近の `})();` 直後）に挿入する。**`window.altpocketToast = toast`
        行（line 58 付近）の直後に置いてはならない**: `confirm` const はまだ宣言前で TDZ
        ReferenceError で JS 全体が停止する（過去のレビューで指摘済みの落とし穴）
      - `window.altpocketNormalizeTagName = normalizeTagName;` を `const normalizeTagName =
        (value) => { ... };`（現状 line 344 付近）の **直後の行**に挿入する
      - **シグネチャ規約**: `window.altpocketConfirm` は **object**（`{ show(title, description,
        onConfirm, actionLabel?, actionClass?) }`）であり関数ではない。`items_bulk_actions.js`
        からは `window.altpocketConfirm.show('一括削除', '<件数> 件を削除しますか？',
        () => { /* approve callback */ }, 'Delete', 'btn-danger')` 形式で呼ぶ。
        `window.altpocketConfirm(message)` という関数呼び出しは不可
      - **フォールバック**: `window.altpocketConfirm` / `window.altpocketNormalizeTagName` が
        undefined の場合、`items_bulk_actions.js` は前者を `window.confirm(message)` ブラウザ
        標準に降格、後者を `value.normalize('NFKC').toLowerCase().trim()` のローカル実装に
        降格して機能を維持する
      - 既存単一アイテム削除 / タグ編集の動線（既に同じ `confirm` / `normalizeTagName` を使用中）
        の挙動には影響しない（global 参照を 2 つ追加するのみ）
    - `[data-items-region]` の `bulkselection:changed` event を listen し:
      - `count > 0` ならツールバー（`[data-bulk-toolbar]`）の `hidden=false` 化 + `data-bulk-count`
        テキスト更新（Req 3.1 / 3.6）
      - `count === 0` なら `hidden=true` 化（Req 3.2）
    - ツールバーの delegated click を捕捉:
      - `button.bulk-delete` → **click ハンドラ冒頭で `const requestIds = selection.getSelectedIDs();`
        を snapshot**（`Array.from(selection.getSelectedIDs())` で defensive copy；後述「リクエスト ID
        スナップショット規約」全項目の前提）。続けて
        `window.altpocketConfirm.show('一括削除', `${requestIds.length} 件を削除しますか？`,
        () => { /* approve → POST /v1/items/bulk-delete with requestIds */ }, 'Delete',
        'btn-danger')` を呼ぶ（**object の `.show(...)` メソッド呼び出し、関数呼び出しではない** /
        Req 4.1）。`window.altpocketConfirm` が undefined ならブラウザ標準 `window.confirm()` に
        降格。cancel / Escape では approve callback が発火しないため、追加コード無しで「キャンセル
        時に何もしない」が成立する（既存 `confirm.show` の挙動）（Req 4.2 / 4.3）。**approve
        callback / fetch / レスポンス処理は closure 内 `requestIds` を参照**し live `selection` を
        使わない
      - `button.bulk-tag` → **click ハンドラ冒頭で `const requestIds = selection.getSelectedIDs();`
        を snapshot**（`Array.from(...)` で defensive copy）。続けて `<dialog data-bulk-tag-dialog>`
        を `showModal()`、フォーム submit で `<input data-bulk-tag-input>` の値を取得。
        submit ハンドラ / fetch / レスポンス処理は closure 内 `requestIds` を参照し live
        `selection` を使わない。dialog title 内の選択件数表記も `requestIds.length` から組み立てる。
        **bulk-tag dialog submit 規約**（round 4 review feedback / `method="dialog"` 自動 close
        対策）: form の `submit` ハンドラ冒頭で **必ず `event.preventDefault()` を呼び**、
        ブラウザのネイティブ dialog close を抑止する。これにより、空判定で no-op となるケース
        （Req 5.9）や 400 invalid_tag レスポンスを返すケース（Req 5.9 二重防御）で dialog が
        意図せず閉じてしまい input に focus 戻しが不可能になる事故を防ぐ。dialog の close は
        以下のいずれの経路に対しても **JS 側から明示的に `dialog.close()` を呼ぶ** ことで完結
        させる:
          - 200 OK（全成功 / 部分失敗いずれも）→ 結果反映後に `dialog.close()`
          - cancel ボタン押下（`data-bulk-tag-cancel`）→ click ハンドラで `dialog.close()`
          - Escape キー → ブラウザの `<dialog>` 標準で `cancel` event 発火 → dialog 自身が close
            （`preventDefault()` の対象外）
          - 空判定 / 400 invalid_tag → close せず focus を `data-bulk-tag-input` に戻す
        **空判定のためだけに**
        `(window.altpocketNormalizeTagName || ((v) => v.normalize('NFKC').toLowerCase().trim()))(value)`
        を実行し、正規化結果が空文字なら no-op + input に focus 戻す（Req 5.9）。
        **POST 時は正規化前の原文字列をそのまま送る**（NFKC + lowercase を JS 側で強制適用しない /
        既存単一アイテム編集では server 側 `normalizeTagInputs` が `Name` に原文字列を保持して
        chip 表示の casing を維持する仕様 / Req 5.2「既存単一アイテム編集と同じタグ正規化規則を
        適用する」）。`POST /v1/items/bulk-tag` の body は `{"item_ids": [...], "tag": <原文字列>}`
      - `button.bulk-clear` → `selection.clear()` を呼ぶ（Req 3.4）
    - **失敗一覧の提示（共通ヘルパー）**: `bulk-failure-dialog`（task 5 で SSR 済みの
      `<dialog data-bulk-failure-dialog role="alertdialog">`）に失敗 item を populate して
      `showModal()` するヘルパー `showBulkFailureDialog({verb, items})` を本モジュール内に持つ。
      `verb` は `"削除"` / `"タグ付け"`、`items` は `[{id, title, url}]` 配列。動作:
      - `data-bulk-failure-title` の `textContent` を `${items.length} 件の${verb}に失敗しました` に更新
      - `data-bulk-failure-list` の子要素を `replaceChildren()` で空に
      - 各 item について `<li>` を `createElement` で生成し、`title` があれば
        `li.textContent = title`、無ければ `li.textContent = url`（**`textContent` のみ使う / XSS
        防御** / NFR 5.1）。両方無い項目は `li.textContent = item.id`（fallback）
      - `<li>` を順次 `appendChild`
      - dialog を `showModal()` で開く（CSS が scrollable + max-height を担保するため 100 件全件
        reachable / Req 4.7 / 5.7「特定可能な形で通知」を 100 件まで満たす）
      - `data-bulk-failure-close` ボタン押下 / Escape で close（既存 `<dialog>` の標準挙動 +
        click handler 1 つを `init()` 時 register）
      - **トースト併用**: 並行で `toast.error('${items.length} 件の${verb}に失敗しました（詳細を開く）')`
        を発火し、件数だけは toast 経由で持続表示（dialog 閉鎖後の reminder）
      - **truncation は行わない**（過去レビューで指摘済み「先頭 3 件 + ほか N 件」は Req 4.7 /
        5.7 違反のため撤廃。100 件分の `<li>` を scrollable 領域で全件提示）
    - **リクエスト ID スナップショット規約**（round 6 review feedback / live selection
      依存の事故防止）: bulk-delete / bulk-tag いずれの click ハンドラも、**fetch 起動の直前** に
      `const requestIds = selection.getSelectedIDs();`（または `Array.from(...)`）で
      **その時点の選択 id を snapshot し、ローカル const に保持**する。以降の **すべての**
      レスポンス処理（成功 / 部分失敗 / 4xx / 5xx / network 失敗）は、live `selection` ではなく
      この `requestIds` snapshot を入力として動作させる。具体的な不変条件:
      1. **dialog 表示の件数表記**: confirm dialog（一括削除）/ bulk-tag dialog の「N 件を…」表記は
         `requestIds.length` から組み立てる（live `selection` の `getSelectedCount()` は使わない）
      2. **selection 解除は snapshot ベース**: 全成功時の解除は **`selection.removeFromSelection(requestIds)`**
         で行い、`selection.clear()` は **使用しない**。これにより、fetch 中にユーザーが別カード
         B を新規選択していても B の選択が誤って消えない（Req 4.8 / 5.8 の「失敗対象の選択保持」
         を fetch 中の新規選択にまで拡張して保証する）。部分失敗時の解除は
         `selection.removeFromSelection(succeeded)`（succeeded はレスポンス由来）で同様
      3. **失敗 dialog の DOM 収集も snapshot ベース**: 4xx / 5xx 全件失敗扱い時の title / URL
         収集は、**`requestIds` の各 id について** `region.querySelector('article[data-item-id="<id>"]')`
         を回す。fetch 中に削除済み state タブ切替で article が DOM から消えていた場合は、
         `li.textContent = item.id` の id-only fallback（後述「失敗一覧の提示」節と同じ）で扱う
      4. **succeeded の id-only fallback**: 200 OK 全成功時の DOM 削除も、`succeeded`（レスポンス
         由来 / `string[]`）の各 id について `region.querySelector('article[data-item-id="<id>"]')`
         が **null** を返した場合（fetch 中の状態タブ切替等で当該 article が現在 view 外）は、
         fade-out を no-op として skip し、`selection.removeFromSelection([<id>])` のみ実施する
      5. **失敗 id-only fallback**: 部分失敗時の `failed[].item_id` または 4xx/5xx 全件失敗時の
         `requestIds` から DOM 収集する経路で、`region.querySelector` が null を返した場合は、
         `collectedFailures` に `{id: <item_id>, title: null, url: null}` を push し、
         `showBulkFailureDialog` 内の既存 fallback（`li.textContent = item.id`）で id だけを
         提示する。`region.querySelector` が null 戻り時に collection を skip すると Req 4.7 /
         5.7「失敗対象を特定可能な形」の通知が脱落するため、id-only でも必ず 1 行は提示する
      6. **fetch 中 selection 操作は凍結しない**: per-card checkbox / Shift+クリック範囲選択 /
         `x` ショートカット / 状態タブ切替 / 検索クエリ / タグフィルタ / ソート / ページ送りは
         fetch 中も従来どおり許可する（既存 busy 状態の対象はツールバー / dialog 操作ボタンのみ /
         後述「busy 状態」節と整合）。snapshot 規約により live selection 変動はレスポンス処理に
         **影響しない** ため、UX を不必要に縛らず棚卸し作業中の操作性を維持する
    - **一括削除レスポンス処理**:
      - レスポンス型の前提: `BulkDeleteResponse.succeeded` は **`string[]`**（id の配列）、
        `BulkDeleteResponse.failed` は **`BulkFailureDetail[]`**（`{item_id, reason}` のみ /
        **`title` / `url` フィールドは struct 自体に存在しない** / design.md Components 節の
        最終仕様）。実装では succeeded を `string[]` として直接走査し、failed のみ
        `failed[i].item_id` でアクセスする
      - 200 OK + 全成功: succeeded（`string[]`）の各 id について
        `region.querySelector('article[data-item-id="<id>"]')` を fade-out（後述
        「fadeOutAndRemove と beginActionMutation のブラケット規約」参照）で削除（querySelector
        null 時は id-only fallback / snapshot 規約 4）、`selection.removeFromSelection(requestIds)`
        後にツールバー隠す（Req 4.4 / 4.5 / 4.6 / 4.8 fetch 中新規選択保持）、
        `toast.success('N 件削除しました')`
      - 200 OK + 部分失敗:
        1. **DOM 削除前に failed 詳細を収集**: `failed[].item_id` ごとに対応する article
           （`region.querySelector('article[data-item-id="<failed.item_id>"]')`）から
           `h3[id^="item-title-"]` の textContent を `title`、**`article.dataset.originalUrl`**
           （task 5 で SSR された `<article data-original-url="{{.URL}}">` の値）を `url` として
           抽出する。querySelector が null（fetch 中の状態タブ切替等で article が現在 view 外）
           なら `{id: failed.item_id, title: null, url: null}` を collectedFailures に push
           （snapshot 規約 5 / id-only fallback で 1 行は提示）。**`.tile-link[href]` は使わない**:
           既存テンプレ `templates/items_list.html` の `<a class="tile-link" href="/ui/items/<id>">`
           は内部詳細ページ URL であり元記事 URL ではないため、タイトル空 item で `url` fallback
           として提示すると元記事を特定できなくなり Req 4.7 / 5.7 違反となる。actions 側で
           failed item は remove しないが、収集順を **succeeded 削除より前** に揃えて順序依存を
           回避
        2. succeeded を beginActionMutation/endActionMutation ブラケット内で DOM 削除（null 時は
           snapshot 規約 4 の id-only fallback）
        3. `selection.removeFromSelection(succeeded)`、failed[].item_id は selection 残置（Req 4.8）
        4. `showBulkFailureDialog({verb: '削除', items: collectedFailures})` を呼ぶ（Req 4.7）
      - 4xx / 5xx（全件失敗扱い、ネットワーク失敗を含む）: **`requestIds` snapshot の各 id** に
        ついて DOM から title / URL を収集（snapshot 規約 3 / null 時は id-only fallback で 1 行
        必ず提示）→ `showBulkFailureDialog({verb: '削除', items})` を呼ぶ（Req 4.7「失敗の一部
        または全部」を fetch 中新規選択 B の混入なしで満たす）。selection は触らない（残置）
      - 400 invalid_request / 400 payload_too_large: `toast.error` で「リクエストが不正です」/
        「100 件を超える選択はできません」を表示。selection は保持（人間 identify が不要な
        systemic エラーのため failure dialog は出さない）
    - **一括タグ付けレスポンス処理**:
      - 200 OK + 全成功: succeeded[].tags を当該カードの `.tags` chip 列に反映する。**chip ノード
        は既存 SSR と同じ contract**（`items_list.html` line 65-70 と一致）で組み立てる:
        - **active タグフィルタ集合の事前算出**（canonical `tag=` repetition + legacy
          `tags=csv` 両形式対応 / round 4 review feedback）: chip 構築前に
          `const params = new URL(window.location.href).searchParams;` を取得し、以下を順次
          concat して active tag name の生集合を作る:
          ```javascript
          const rawTagNames = [
            ...params.getAll('tag'),                                  // canonical 形式
            ...(params.get('tags')?.split(',') ?? []),                // legacy CSV 形式
          ];
          ```
          既存 server `parseTagFilters`（`internal/server/server.go:1557` 付近）が両形式を
          受理する規約を JS 側でミラーするため両形式を見る。SSR の `buildTagRemovedURL` は
          canonical `?tag=` repetition への migration を行うが、初回 page load 直後の URL や
          bookmark / 手動 URL 入力経路で `?tags=go,rust` が残ることがある。各要素を
          `(window.altpocketNormalizeTagName || ((v) => v.normalize('NFKC').toLowerCase().trim()))(name)`
          で正規化し、空文字を除外した値の `Set<string>` を作る（`activeNormalizedNames`）。
          1 回の chip 再構築サイクルで複数 card に適用するため、card ループの外で 1 度だけ算出する
        - tag 要素: `document.createElement('button')`
        - `setAttribute('type', 'button')`
        - **class / aria-pressed の active 一致判定**: `const isActive =
          activeNormalizedNames.has(tag.normalized_name);` を計算し、true なら
          `setAttribute('class', 'tag tag-filter-toggle is-selected')` + `setAttribute('aria-pressed', 'true')`、
          false なら `setAttribute('class', 'tag tag-filter-toggle')` + `setAttribute('aria-pressed', 'false')`
          とする（既存 SSR の `SelectedTags` 判定を JS 側で再現 / NFR 3.2 active-filters chip 連携 /
          NFR 3.3 後方互換 / round 2 review feedback）
        - `setAttribute('data-tag-filter-toggle', '')`（空属性）
        - `setAttribute('data-tag-normalized', tag.normalized_name)`
        - `setAttribute('aria-label', 'タグで絞り込み: ' + tag.name)`
        - `button.textContent = tag.name`（**`innerHTML` / `insertAdjacentHTML` は禁止** /
          XSS 防御 / NFR 5.1）
        - 既存 `<div class="tags">` を `replaceChildren(...newButtons)` で全置換（既存 chip 列が
          無い card は `<div class="tags">` を createElement で挿入してから append）
        これにより #117 chip クリック絞り込みと #115 active-filters chip 連携が新規付与タグでも
        動作する（NFR 3.3 後方互換）。タグフィルタ中（例: `?tag=GoLang` で絞り込み中）に
        bulk-tag 成功で chip が再構築されても、フィルタ中タグの `is-selected` / `aria-pressed=true`
        状態が新規付与タグ列でも保持される（round 2 review feedback）
      - `selection.removeFromSelection(requestIds)` + dialog 閉鎖 + ツールバー隠す（Req 5.5 /
        5.6 / 5.8 fetch 中新規選択保持 / snapshot 規約 2）
      - 200 OK + 部分失敗: succeeded の tags を反映（上と同じ DOM API 経路 / querySelector
        null 時は chip 反映を skip）+ `selection.removeFromSelection(succeeded.map(s => s.item_id))`、
        failed は selection 残置（Req 5.8）+ `showBulkFailureDialog({verb: 'タグ付け', items})`
        （DOM 収集規約は削除と同じ snapshot 規約 5 適用 / Req 5.7）
      - 400 invalid_tag: dialog open のまま + 入力欄に focus 戻す + `toast.error('タグ名を入力して
        ください')`
      - **400 invalid_request / 400 payload_too_large**（systemic エラー扱い / per-item identify
        が不要 / design.md「Client-side error handling」400 invalid_request / payload_too_large
        節と整合 / round 5 review feedback）: **`toast.error` のみ**で `bulk-failure-dialog` は
        出さない。`toast.error('一括タグ付けのリクエストが不正です')` / `toast.error('一括タグ付け
        の対象が多すぎます（最大 100 件）')` 等の具体的メッセージを 1 行で出す。**selection は
        触らない**（一括削除側の同経路と挙動を一致させる）
      - 4xx 他（401 / 403 / 429 等）/ 5xx（全件失敗扱い）: 一括削除と同じく **`requestIds`
        snapshot の各 id** から DOM 収集して `showBulkFailureDialog({verb: 'タグ付け', items})`
        を表示（Req 5.7 / snapshot 規約 3 / null 時 id-only fallback）。selection は触らない
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
    - **busy 状態**（NFR 1.2 / round 4 review feedback: pointer-events だけではキーボード
      起動を止められない）: click 直後に以下の **両方** を実施し、応答完了で両方を解除する:
      1. ツールバー（`[data-bulk-toolbar]`）に `is-busy` class を付与する（CSS task 8 が
         spinner / 視覚的 dim を即時表示）
      2. ツールバー内の **全ての操作ボタン**（`button.bulk-delete` / `button.bulk-tag` /
         `button.bulk-clear`）および bulk-tag dialog の操作ボタン（`button[data-bulk-tag-cancel]` /
         `button[data-bulk-tag-confirm]`）に **`disabled` 属性を `setAttribute`** で付与する
         （`button.disabled = true` でも可）。これにより `pointer-events: none` の CSS では
         止められないキーボード起動（Tab フォーカス → Enter / Space）まで含めて二重送信を確実に
         抑止する。応答完了で `removeAttribute('disabled')` する
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
    - `TestDeleteAllSuccessRemovesCardsAndDeselectsSnapshot`: 全成功レスポンス → 該当 article が
      DOM から削除 + `selection.removeFromSelection(requestIds)` が呼ばれる（**`selection.clear()`
      は呼ばれない** / snapshot 規約 2 / Req 4.5 / 4.6）
    - `TestDeleteAllSuccessPreservesInFlightNewSelection`: bulk-delete click 時 selection が
      [A, B] → confirm 承認後 fetch pending 中にユーザーが card C を新規選択し selection が
      [A, B, C] になる → 200 OK 全成功（succeeded=[A, B]）レスポンス処理後、selection に C が
      残置されている（A / B のみ解除）。**`selection.clear()` を呼ぶ古い実装では C も消える** /
      Req 4.8 を fetch 中新規選択にまで拡張 / round 6 review feedback の回帰固定
    - `TestDeletePartialFailureKeepsFailedSelected`: 部分失敗レスポンス → succeeded の card は
      DOM 削除、failed の id は selection に残置 + `bulk-failure-dialog` が `showModal` で開き
      `<li>` に失敗 item の title (or url) が **全件** 含まれる（truncation 無し / Req 4.7 / 4.8）
    - `TestDeleteCancelDoesNothing`: confirm cancel（`window.altpocketConfirm.show` の approve
      callback が呼ばれない経路）→ fetch 未呼出 / 選択保持（Req 4.3）
    - `TestDeleteConfirmUsesShowSignature`: bulk-delete click 時、actions モジュールが
      `window.altpocketConfirm.show(title, description, onConfirm, actionLabel, actionClass)` の
      **object メソッド呼び出し**を行うことを spy で assert（**`window.altpocketConfirm(message)`
      の関数呼び出しは行わない**）。`description` 引数に件数が含まれることも assert
      （Req 4.1 / シグネチャ規約の回帰固定）
    - `TestDeleteRateLimitedShowsFailureDialog`: 429 `{"error":"rate_limited"}` レスポンス →
      `bulk-failure-dialog` が **`requestIds` snapshot 全件分**の title/url を `<li>` で列挙して
      open + selection は残置（Req 4.7「失敗の全部」の 4xx 経路 / snapshot 規約 3）
    - `TestDeleteServerErrorUsesRequestIdsNotLiveSelection`: bulk-delete click 時 selection が
      [A, B] → fetch pending 中にユーザーが card C を新規選択 / card A を解除し selection が
      [B, C] になる → 500 db_error レスポンス → `bulk-failure-dialog` の `<li>` 列挙が
      [A, B]（**snapshot 由来**）であり、C は含まれず、解除済み A は含まれる。selection は
      触らない（[B, C] のまま）。**live `selection` を見る古い実装では [B, C] が列挙されて
      C の title/url が誤って通知され A は脱落する** / Req 4.7 を snapshot ベースで厳密化 /
      round 6 review feedback の回帰固定
    - `TestDeleteServerErrorShowsFailureDialog`: 500 `{"error":"db_error"}` または fetch reject
      （network 失敗）→ 同上の全件失敗ダイアログ + selection 残置（Req 4.7 5xx 経路）
    - `TestDeleteForbiddenBearerRejectShowsFailureDialog`: 403 `{"error":"forbidden"}` → 同上
      （拡張機能 / MCP からの呼び出しが万一通った場合の表示一貫性）
    - `TestDeleteUnauthorizedShowsFailureDialog`: 401 `{"error":"unauthorized"}` → 同上
    - `TestDeleteInvalidRequestShowsToastNotDialog`: 400 `{"error":"invalid_request"}` →
      **`toast.error` のみ**で `bulk-failure-dialog` は出さない（systemic エラーで per-item identify
      が不要 / selection 保持）
    - `TestDeletePayloadTooLargeShowsToastNotDialog`: 400 `{"error":"payload_too_large"}` → 同上
    - `TestTagButtonOpensDialog`: bulk-tag click → `<dialog>` open
    - `TestTagDialogEmptyInputIsNoOp`: 空文字 / 全角空白だけ入力 → fetch 未呼出（Req 5.9）+
      input に focus 戻す。`window.altpocketNormalizeTagName` undefined 時のフォールバック
      （`value.normalize('NFKC').toLowerCase().trim()`）でも同じ判定が成立することを併せて assert
    - `TestTagDialogConfirmCallsAPI`: 非空入力 → fetch が `/v1/items/bulk-tag` を呼ぶ、body に
      `item_ids` / `tag`（**原文字列、normalize していない**）を含む（Req 5.2 既存規則踏襲）
    - `TestTagSuccessRebuildsChipsWithFilterToggleContract`: 全成功レスポンス → succeeded[].tags
      が当該カードの `.tags` chip 列に反映され、各 chip 要素が **`button.tag.tag-filter-toggle`
      + `data-tag-filter-toggle` 属性 + `data-tag-normalized="<normalized>"`
      + `aria-label="タグで絞り込み: <name>"` + textContent=<name>** をすべて持つことを assert。
      URL に active タグフィルタが無い場合は **すべての chip が `aria-pressed="false"` + class
      が `tag tag-filter-toggle`（is-selected なし）** であることを併せて確認
      （Req 5.5 + NFR 3.3 #117 chip クリック絞り込み契約の維持 / 過去レビュー: chip rebuild
      契約欠落の回帰固定）
    - `TestTagSuccessPreservesActiveFilterChipSelectedState`: `window.location` を
      `?tag=GoLang&tag=Rust` で stub し、全成功レスポンスの `succeeded[].tags` に `golang` /
      `rust` / `python` を含むケース → 再構築後の `.tags` chip 列のうち `data-tag-normalized="golang"`
      と `"rust"` の chip が `class="tag tag-filter-toggle is-selected"` + `aria-pressed="true"`、
      `"python"` の chip は `class="tag tag-filter-toggle"` + `aria-pressed="false"` であることを
      assert（NFR 3.2 active-filters chip 連携の維持 / round 2 review feedback）。さらに、URL の
      tag 値が全角混じり等で SSR 側 normalize と一致するか確認するため、URL に `?tag=ＧｏＬａｎｇ`
      （全角）を stub したケースで `data-tag-normalized="golang"` chip が `is-selected` になる
      ことも併せて assert（`window.altpocketNormalizeTagName` または fallback 経路の同期性回帰固定）
    - `TestTagSuccessRespectsLegacyTagsCsvParam`: `window.location` を **legacy CSV 形式**
      `?tags=go,rust` で stub し、全成功レスポンスの `succeeded[].tags` に `go` / `rust` /
      `python` を含むケース → `data-tag-normalized="go"` と `"rust"` の chip が `is-selected` +
      `aria-pressed="true"`、`"python"` の chip は `is-selected` 無 + `aria-pressed="false"`
      になることを assert。canonical `?tag=` repetition と legacy `?tags=csv` の **両形式** で
      active filter chip の状態維持が同等に動作する（既存 server `parseTagFilters` の両形式
      受理規約と一致する JS 側ミラー / round 4 review feedback）。canonical と legacy の
      混在ケース（`?tag=go&tags=rust,python`）も併せて 1 ケース追加し、両方の tag が
      active 集合に合流することを assert
    - `TestTagSuccessDeselectsSnapshotAndClosesDialog`: 全成功 →
      `selection.removeFromSelection(requestIds)` + dialog 閉鎖（**`selection.clear()` は呼ばれない** /
      snapshot 規約 2 / Req 5.6）。bulk-tag click 時 [A] → dialog open → tag 入力 → fetch
      pending 中にユーザーが card B を新規選択 → 200 OK → selection に B が残置されている
      （fetch 中新規選択保持 / Req 5.8 拡張 / round 6 review feedback の回帰固定）
    - `TestTagPartialFailureKeepsFailedSelected`: 部分失敗 → succeeded の chips 反映 + failed の id
      は selection 残置 + `bulk-failure-dialog` で failed 全件列挙（Req 5.7 / 5.8）
    - `TestTagRateLimitedShowsFailureDialog`: 429 → `bulk-failure-dialog` + selection 残置
      （Req 5.7 4xx 経路）
    - `TestTagServerErrorShowsFailureDialog`: 500 / network 失敗 → 同上（Req 5.7 5xx 経路）
    - `TestTagInvalidTagOpenedDialogStaysAndFocusInput`: 400 invalid_tag → bulk-tag dialog 開いた
      まま + `data-bulk-tag-input` に focus 戻す + `toast.error('タグ名を入力してください')`
      （**`bulk-failure-dialog` は出さない**）
    - `TestTagInvalidRequestShowsToastNotDialog`: 400 `{"error":"invalid_request"}` →
      **`toast.error` のみ**で `bulk-failure-dialog` は出さない（systemic エラーで per-item
      identify が不要 / selection 保持 / 一括削除側の `TestDeleteInvalidRequestShowsToastNotDialog`
      と挙動一致 / round 5 review feedback）
    - `TestTagPayloadTooLargeShowsToastNotDialog`: 400 `{"error":"payload_too_large"}` → 同上
      （一括削除側の `TestDeletePayloadTooLargeShowsToastNotDialog` と挙動一致）
    - `TestFailureDialogPopulatesAllItemsWithoutTruncation`: 失敗 6 件 / 50 件 / 100 件の dialog
      populate で `<li>` 件数がそのまま 6 / 50 / 100 になることを assert（truncation 撤廃の
      回帰固定 / Req 4.7 / 5.7）
    - `TestFailureDialogUsesTextContentNotInnerHTML`: failed item の title に `<script>` を含む
      文字列でも、`<li>` に script 要素として挿入されない（`textContent` 使用 / XSS 防御 /
      NFR 5.1）
    - `TestToolbarShowsHidesOnSelectionChange`: `bulkselection:changed` event detail.count=0 → hidden、
      count>0 → 表示 + 件数テキスト更新（Req 3.1 / 3.2 / 3.6）
    - `TestClearButtonCallsSelectionClear`: bulk-clear click → selection.clear が呼ばれる（Req 3.4）
    - `TestToolbarButtonsDisabledDuringInFlightRequest`: bulk-delete click → fetch が pending な間、
      ツールバー内の全ボタン（`button.bulk-delete` / `button.bulk-tag` / `button.bulk-clear`）が
      `disabled` 属性を持つことを assert（pointer-events のみではキーボード起動を止められないため
      button disabled で二重送信を防ぐ規約の回帰固定 / NFR 1.2 / round 4 review feedback）。
      fetch resolve 後（成功 / 失敗いずれも）に `disabled` が外れることを併せて assert
  - **テスト追加（同 task 内）**: 上記 30 件の actions モジュールテストを本タスクで完結させる
    （Req 3.1 / 3.2 / 3.3 / 3.4 / 3.6 / 4.1〜4.8 / 5.5〜5.9 / 6.5 / NFR 1.2 の同 task 内テスト
    必須カテゴリに該当、4xx/5xx エラーパス・chip rebuild 契約・confirm シグネチャ規約・failure
    dialog 全件 populate・legacy `?tags=csv` 形式の active filter chip 連携・busy 状態の
    button disabled・**bulk-tag 400 invalid_request / payload_too_large が toast のみで dialog
    を出さない**（一括削除側と挙動一致）の回帰固定を含む）
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.6, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 5.1, 5.2, 5.5, 5.6, 5.7, 5.8, 5.9, 6.5, NFR 1.2, NFR 1.3_
  - _Boundary: Static_
  - _Depends: 5, 6_

- [ ] 8. CSS: チェックボックス + 選択カード視覚区別 + 選択ツールバー + タグ入力 dialog
  - **既存 token のみを使用する**（round 4 review feedback: `--focus-ring` / `--bg-selected`
    / `--border-primary` / `--border-default` / `--font-size-base` は **本リポジトリの
    `static/style.css` に未定義** であるため使用不可）。本リポジトリで実在する token は以下
    （`grep -oE -- '--[a-zA-Z0-9-]+' static/style.css | sort -u` で随時確認可）:
    - 余白: `--space-1` / `--space-2` / `--space-3` / `--space-4` / `--space-5` / `--space-6` ほか
    - 色: `--color-primary` / `--color-primary-hover` / `--color-primary-soft` /
      `--color-danger` / `--color-success` / `--color-info`
    - 背景: `--bg-base` / `--bg-surface` / `--bg-elevated` / `--bg-grouped`
    - テキスト: `--text-primary` / `--text-secondary` / `--text-tertiary` / `--text-quaternary`
    - 区切り: `--separator` / `--separator-opaque`
    - radius: `--radius-sm` / `--radius-md` / `--radius-lg` / `--radius-full`
    - shadow: `--shadow-sm` / `--shadow-md` / `--shadow-lg` / `--shadow-sheet`
    - typography: `--type-body-size` / `--type-caption-1-size` / `--type-headline-size` ほか
    - motion: `--motion-fast` / `--motion-moderate` / `--motion-standard` /
      `--ease-default` / `--ease-spring` / `--ease-accelerate` / `--ease-decelerate`
  - `static/style.css` に以下を追加（**未定義 token は使わない**。表現が必要な場合は既存 token を
    組み合わせる、または `outline: 2px solid var(--color-primary); outline-offset: 2px;` のような
    リテラルで代替する）:
    - `.item-select`: カード左上に位置するチェックボックス（`position: absolute; top: var(--space-2);
      left: var(--space-2);` 程度）。フォーカスリングは
      `:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }` の
      リテラル表現で揃える（既存 `.tag-filter-toggle:focus-visible` 等の outline 表現と一致）。
      `.item-card` 側は `position: relative` を確保
    - `.item-card.is-selected`: 選択中の視覚区別。背景は `--color-primary-soft`（既存
      primary 系の弱色）、強調は `outline: 2px solid var(--color-primary); outline-offset: -2px;`
      または `box-shadow: inset 0 0 0 2px var(--color-primary);` で表現する（既存 `.failed`
      border-left との衝突を避ける / Req 1.4 視覚区別を色だけに依存しない – checked 状態の
      checkbox との併用で十分）
    - `.bulk-toolbar`: `position: sticky; bottom: 0;`（または fixed bottom）でスクロール中も画面下に
      固定（Req 3.5）。背景 `--bg-elevated` + 上端 `border-top: 1px solid var(--separator-opaque);`
      で本文と区別、`padding` は `var(--space-3) var(--space-4);`、`hidden` 属性が付いている間は
      `display: none`
    - `.bulk-toolbar-count`: 件数表示の typography（`font-size: var(--type-body-size);
      color: var(--text-secondary);`）
    - `.bulk-toolbar-actions`: ボタン 3 個の横並びレイアウト（`display: flex; gap: var(--space-2);`）
    - **busy 状態の表現は CSS 単独で完結させない**（round 4 review feedback: `pointer-events: none`
      ではキーボード起動を止められない）: actions モジュール（task 7）が click 直後に各
      `<button>` に `disabled` 属性を `setAttribute` で付与し、応答完了で除去する。CSS 側は
      `:disabled` 疑似クラスに対する visual style のみを担当し、`cursor` / `opacity` /
      `aria-disabled` 連動の視覚効果を与える:
      - `.bulk-toolbar button:disabled, .bulk-tag-dialog button:disabled { opacity: 0.65;
        cursor: not-allowed; }`（ボタン disabled で `pointer-events: none` も自動的に適用される
        ブラウザ標準挙動を活用）
      - `.bulk-toolbar.is-busy` は spinner / overlay 等の **視覚的補助**（disabled 表現を補強
        する目的）に限定して使う（`is-busy` 単独ではキーボード起動を抑止できないため、必ず
        button disabled と併用する）
    - `.bulk-tag-dialog`: ネイティブ `<dialog>` の最低限スタイル（既存 `confirm-overlay` の
      backdrop / shadow / radius トークンに揃える: `background: var(--bg-elevated);
      border-radius: var(--radius-md); box-shadow: var(--shadow-lg);` 等）
    - `.bulk-tag-dialog::backdrop`: dialog 背景 dim（既存 confirm overlay と同じ rgba。既存
      `dialog::backdrop` の rgba 値を流用）
    - `.bulk-failure-dialog`: 失敗一覧 dialog のレイアウト（`max-width: min(560px, 90vw);` 程度
      + `padding: var(--space-4); border-radius: var(--radius-md); background: var(--bg-elevated);
      box-shadow: var(--shadow-lg);`、`confirm-overlay` と同じ backdrop / shadow / radius
      トークンに揃える）
    - `.bulk-failure-dialog::backdrop`: dialog 背景 dim（同上 rgba）
    - `.bulk-failure-list`: **`max-height: 60vh; overflow-y: auto;`** + 余白 + 単純な
      list-style（失敗 100 件まで scrollable に全件 reachable / Req 4.7 / 5.7 truncation 廃止
      の CSS 側保証）。`<li>` 内テキストは長いタイトル / URL を折返し可能とする
      （`overflow-wrap: anywhere;`）
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
  - _Requirements: 1.4, 1.5, 3.5, 4.7, 5.5, 5.7, NFR 1.1, NFR 1.3, NFR 4.3_
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
8.3 の store 層検証は、タスク 2（store integration test）で **解消** する。タスク 3 は
`_Requirements_partial:_` を持たず、handler 層の認可境界・部分失敗・構造化ログ・DB エラー 500・
UUID collapse・request body のバイト境界を **fake `bulkItemsStore` interface 経由の unit テスト**
で同 task 内に閉じる（round 4 review feedback / CI 実行可能な `go test ./...` 経路で退行検出）。
タスク 4（server integration test）は task 3 の deferred test 解消ではなく、**実 DB を介した SQL
経路の退行検出**（store の WHERE user_id 条件 / RETURNING の認可境界）を担当する。per-task Reviewer
運用時は、タスク 2 を「タスク 1 の deferred test を解消する dedicated regression test task」と
して扱う（`.claude/rules/tasks-generation.md` 「task-test 境界整合の規約」参照）。タスク 4 は
タスク 3 と並行で integration を補完する独立 task として位置付ける。
