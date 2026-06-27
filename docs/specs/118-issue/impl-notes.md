# Implementation Notes

本 spec は per-task ループで実装される。各 task の learning を時系列で記録する。

## Implementation Notes

### Task 1
- 採用方針: design.md「Components and Interfaces > Store Layer」の SQL 仕様を逐語的に実装し、`internal/store/items_bulk.go` 1 ファイルで `BulkDeleteItems` / `BulkAddItemTag` / `BulkTagResult` を提供。既存 `internal/store/store.go` / `tags.go`（後者は repo 上に存在しないため触らず、`TagInput` 型は `store.go` line 86-89 に定義済）は変更しない。
- 重要な判断:
  - `tags` テーブルへの INSERT は既存 `upsertItemTags`(store.go line 376-398）の慣用句を踏襲し `INSERT INTO tags (name, normalized_name) VALUES ($1, $2)` の 2 列指定（`id` は schema の DEFAULT `gen_random_uuid()` に委ねる）。tasks.md 本文は `INSERT INTO tags (id, name, normalized_name) VALUES (gen_random_uuid(), ...)` を例示していたが、既存 store 規約と差異が出ない同等な形にした（DB 動作・RETURNING 結果は同一）。
  - `SELECT ... FOR KEY SHARE` の所有確認では、scan を完了してから `rows.Close()` を呼ぶ pgx v5 の慣用パターンに従い、defer は使わず明示 close + Err() チェックで実装。tx 上の rows は `tx.Query` の後続クエリと衝突するため defer close では順序保証が脆い。
  - `BulkAddItemTag` の step 4 で `unnest($3::uuid[])` の引数を `ownedItemIDs`（step 1 の SELECT 結果）に絞ったので、items への FK 違反は構造上発生しない。step 1 の KEY SHARE ロックが commit までこれら row の存在を保証する。
  - `item_tags.display_name` は migration 006 で追加された per-user 表示名列。`BulkAddItemTag` の INSERT 文では `tagInput.Name`（normalize 前の表示名）を `display_name` に書き、`ON CONFLICT DO NOTHING` で既存行があれば書き換えないようにした（Req 5.4 重複なし、かつ既存表示名 / casing を保持）。これは既存 `CreateItem` の `upsertItemTags` が `ON CONFLICT DO UPDATE SET display_name=EXCLUDED.display_name` で更新する規約とは異なるが、本 task は「追加のみ（既存タグは保持）」という Req 5.3 / 5.4 の方針に合わせて `DO NOTHING` を採用（design.md の SQL 仕様と一致）。
- 残存課題: なし。task 2 で integration test が追加されると、SQL の正しさ（per-user 分離 / 部分失敗 / 重複防止 / 並行 DELETE での FK race 回避）が実 DB で固定される。

### Task 2
- 採用方針: `internal/store/items_bulk_test.go`（`//go:build integration`、`package store` 同パッケージ）を新規作成し、task 1 の `BulkDeleteItems` / `BulkAddItemTag` の SQL 振る舞いを 11 ケース + 1 サブテスト（FOR KEY SHARE 互換性）の合計 12 ブロックで固定。`mcp_api_key_test.go` の `seedTestUser` を素のまま再利用するとテスト内で複数ユーザーを seed するときに `users.google_sub` UNIQUE 衝突が起きるため、`tags_lookup_test.go` の `seedTagsLookupUser` 流に **label 付き helper（`seedBulkUser`）** を本ファイル内で独自定義し、TEST_DATABASE_URL 共有時の concurrent run も含めて安全側に倒した。
- 重要な判断:
  - **FOR KEY SHARE 並行 DELETE block の検証方針**: tasks.md は「テスト用 hook で α を pause させる」案を例示していたが、`Store` 構造体に hook を挿入する案は production 面侵入のため不採用とし、tasks.md 末尾に書かれた **方針 A**（pool から手動 tx を 1 つ開いて step 1 と同じ `SELECT ... FOR KEY SHARE` を保持し、別 goroutine から `DELETE` を発火して block を観測する）を採用。これは `BulkAddItemTag` 内部の lock 経路ではなく **PostgreSQL の FOR KEY SHARE 挙動そのもの**の固定だが、本 task の race 閉鎖回帰の要旨を満たす。
  - **block 判定の手段**: 当初 `pg_locks` の query も検討したが、PostgreSQL の lock state は同一接続から見えない / lock 種別の文字列が version で揺れる等の脆さがあり、**`select { case <-deleteDone: t.Fatal case <-time.After(300ms): }` の timeout ベース**を採った（tasks.md にも併記された手段）。alpha commit 後の解放確認は 2s の timeout を許容し、CI 上の負荷揺れを吸収。
  - **副次サブテスト「2 つの FOR KEY SHARE が deadlock しない」**: design.md「step 1 の FOR KEY SHARE は KEY SHARE 同士で互換」という前提を、別 tx ペアで実 DB 上に固定するサブテストを追加。これにより、将来 `FOR KEY SHARE` を `FOR UPDATE` や `FOR NO KEY UPDATE` に書き換える regression を `tx2 が 1s 以内に取れない` 形で検出できる。
  - **seedBulkItemWithTag の cleanup**: `tags_lookup_test.go` の `createUserItemWithDisplayTag` と同じ NOT EXISTS guard 付き orphan tag cleanup を踏襲。共有 TEST_DATABASE_URL での concurrent run でも他テストの item_tags を切断しない（PR #137 round 6 規約と整合）。
  - **`existsTagByNormalized` で EARLY RETURN ガード固定**: `TestBulkAddItemTag_AllNotOwnedDoesNotCreateTagsRow` は design.md「BulkAddItemTag EARLY RETURN ガード」節（task 1 の step 2）の意図的副作用回避を実 DB で固定する。`ghost-tag` 正規化名で「呼出前に存在しない」を pre-condition assert → 全 id が認可違反 / 不存在 → 呼出後も存在しないことを確認、という流れで設計と挙動を 1:1 で繋いだ。
- 残存課題: なし。store 層の per-item 成功/失敗の振る舞い検証は本 task で完結し、task 1 の `_Requirements_partial:_`（4.4 / 4.5 / 5.3 / 5.4 / 8.1 / 8.2 / 8.3）の deferred は本 task で解消された。後続 task 3 以降の handler 層・integration 層は別軸の検証として続く。

### Task 3
- 採用方針: design.md「Components and Interfaces > Server Layer」と「Handler-side store interface」節に従い、`internal/server/items_bulk.go`（handler + 共通 helper + interface 定義 + request/response 型）と `internal/server/items_bulk_test.go`（25 ケースの unit テスト）を新規追加。`internal/server/server.go` には Server struct への `bulkStore bulkItemsStore` フィールド追加・`New()` 末尾での `s.bulkStore = st` 代入・`/v1/items` route 内の 2 行追加（`POST /bulk-delete` / `POST /bulk-tag`）のみを行い、既存 handler / extension_contract_test.go / items_status.go / 単一アイテム API には一切手を入れない。
- 重要な判断:
  - **`bulkStore` interface 経由の dispatch**: handler から `s.store` を直接呼ばず `s.bulkStore` 経由で呼ぶ点が design.md の核心。`*store.Store` がメソッドシグネチャ一致で interface を満たすため adapter コード不要、既存 `store` field は温存。これにより 25 件中 8 件の fake-store ベース unit test が CI 実行可能な `go test ./...` 経路で認可境界 / 部分失敗 / 構造化ログ振る舞いを退行検出できる。
  - **validation 順序の厳守**: ①Bearer 遮断 → ②auth context → ③`s.limiter.Allow` → ④`http.MaxBytesReader` → ⑤decode + `*http.MaxBytesError` 分類 → ⑥`item_ids` 空/超過 → ⑦UUID per-id collapse → ⑧`tag.Normalize` 空判定（bulk-tag のみ）→ ⑨store 呼出。とくに「empty item_ids は先に invalid_request に倒れる（tag 検証に到達しない）」順序を tasks.md line 238-249 / round 2 review feedback で固定しており、`TestHandleBulkTagItems_EmptyTagReturns400InvalidTag` は valid な item_id を含めて dispatch する。
  - **`BulkFailureDetail` の leak 防止**: struct に `Title` / `URL` フィールドを持たせず、`{ItemID, Reason}` の 2 フィールドだけにしている。`reason` は v1 では `"not_found"` のみ（owned by other user / deleted / invalid uuid を全て collapse）。テスト側で response JSON 文字列に `"title"` / `"url"` が含まれないことを 2 ケース（Delete / Tag 部分失敗）で wire-level assert する。
  - **invalid UUID の collapse 規約**: `partitionByUUID` で valid/invalid に振り分け、invalid は store に渡さずに `failed[{reason:"not_found"}]` に合流する。`computeFailedIDs` は requestIDs の **元の順序を保持**しつつ「succeeded に含まれない or invalid」な id を抽出する（client が送った順序で failed[] が並ぶので request_id correlation が単純になる）。`fake.lastDeleteIDs` / `fake.lastTagIDs` で「invalid uuid が store に到達していない」ことを assert（Req 8.3 / Security Considerations 節）。
  - **body bytes 超過テストの fixture 構築**: tasks.md line 271-301（round 6 review feedback）に従い、`bytes.Repeat([]byte("x"), N)` のような構文不正な byte 列ではなく `json.Marshal` で valid な JSON を組み立てる。bulk-delete は valid UUID を 500 件並べた配列（≈ 19.5 KiB）、bulk-tag は valid な `item_ids` 1 件 + `strings.Repeat("a", 16*1024)` の huge tag。これにより `MaxBytesReader` が境界 16 KiB で発火し `*http.MaxBytesError` が `errors.As` 経由で識別される（`*json.SyntaxError` 経路に倒れない）。テスト中で `len(body) > maxBulkRequestBodyBytes` を assert して fixture の妥当性も確認している。
  - **`tag.Normalize` の挙動依存**: `tag.Normalize("")` / `tag.Normalize("   ")` / `tag.Normalize("　 ")` がすべて空文字を返すことに依存して、invalid_tag を一本化している。`TestHandleBulkTagItems_NormalizationEmptyTagReturns400InvalidTag` で全角空白も含めて固定。tag.Normalize は NFKC + ToLower の sequence で、`tag_normalized` ログフィールドに lowercase 化された値が出る（`GoLang` → `golang`）ので、構造化ログテストではその点も含めて assert（`"tag_normalized":"golang"`）。
  - **構造化ログテストの secret leak guard**: NFR 5.1 の検証として「Cookie: altpocket_session=super-secret-cookie」を request に付けたうえで、`slog` 出力に `super-secret-cookie` / `Bearer` / `altpocket_session` / `Authorization` のいずれも含まれないことを 2 ケース（Delete / Tag）で assert。handler は `slog.String("user_id", ...)` 等 6 フィールドだけを書き、cookie / authorization header / body raw は触っていないので自然に pass する。
  - **rate limit の構成方法**: `ratelimit.New(0, 0)` で `rate=0` / `burst=0` の Limiter を構成すると、`Allow()` の最初の呼び出しから `tokens < 1` で false を返す（`internal/ratelimit/ratelimit.go:41-55`）。`newRateLimitedBulkTestServer` ヘルパーに集約し、Delete / Tag 両方の 429 ケースで共有。
  - **route 登録テストの組み立て**: `newAuthTestServer()` は本機能導入前の状態の `New(...)` を呼ぶため、bulkStore が nil のまま Server を返す（`New()` 内で `s.bulkStore = st` は呼ばれるが `st = nil` のため依然 nil）。`Routes()` は chi.Router を返すだけで handler を invoke しないので nil でも walk 自体は通るが、防御のため `if s.bulkStore == nil { s.bulkStore = &fakeBulkStore{} }` で埋めている。`chi.Walk` でルートツリーを枚挙し `POST /v1/items/bulk-delete` / `POST /v1/items/bulk-tag` の 2 route が登録済みであることを assert。
- 残存課題: なし（task 3 の責務範囲内では全 25 ケース pass）。実 DB 経由の SQL 経路（store の WHERE user_id / RETURNING の認可境界 leak 検証）は task 4 の integration test で別途固定する。templates / static JS / CSS（task 5〜8）は本 task のスコープ外。

## 確認事項

- design.md tasks.md 中の SQL 例（`INSERT INTO tags (id, name, normalized_name) VALUES (gen_random_uuid(), ...)`）と既存 `store.go` の `upsertItemTags` の `INSERT INTO tags (name, normalized_name) VALUES ($1, $2)` の 2 通りの慣用句が併存している。実装側は後者（既存パターン）を採用したが、いずれも DB 動作は同等（schema の DEFAULT に委ねるか明示するかの差）。今後の design レビューで揃えるかどうかは Architect 判断に委ねる。

## AC 達成確認

本タスクは store 層の関数追加のみで、AC の per-item 成功・失敗の振る舞い検証は task 2 の integration test に deferred されている（tasks.md の `_Requirements_partial:_` 4.4, 4.5, 5.3, 5.4, 8.1, 8.2, 8.3 と整合）。本 task で実装した SQL が下記の AC を満たす設計となっていることを確認:

- **Req 4.4 (一括削除)**: `BulkDeleteItems` が `WHERE id = ANY($1::uuid[]) AND user_id = $2 RETURNING id` で own 分のみ削除し succeeded を返す
- **Req 4.5 (成功削除分は退場)**: `RETURNING id` の集合が succeeded となり、client が DOM 退場の対象を特定可能
- **Req 5.3 (タグ追加)**: `BulkAddItemTag` の step 4 が `INSERT INTO item_tags (item_id, tag_id, display_name) SELECT id, $1, $2 FROM unnest($3::uuid[]) AS id` で全 owned item にタグ付与
- **Req 5.4 (重複なし)**: step 4 の `ON CONFLICT (item_id, tag_id) DO NOTHING` で既存タグの 2 重追加を防止
- **Req 8.1 (own のみ操作)**: `BulkDeleteItems` の全 SQL が `user_id = $2` 条件で締まり、`BulkAddItemTag` の step 1 が `WHERE id = ANY(...) AND user_id = $2` で own のみ owned ownership を解決
- **Req 8.2 (認可違反 id は不変)**: 他ユーザー所有 id は SELECT 段階で除外され、後続の DML には渡らない。`BulkAddItemTag` は EARLY RETURN ガードで tags 行の副作用も防ぐ
- **Req 8.3 (削除済み id は不変)**: 存在しない id は SELECT 段階で除外され、succeeded には含まれない

STATUS: complete
