# Implementation Notes

本 spec は per-task ループで実装される。各 task の learning を時系列で記録する。

## Implementation Notes

### Task 1
- 採用方針: design.md「Components and Interfaces > Store Layer」の SQL 仕様を逐語的に実装し、`internal/store/items_bulk.go` 1 ファイルで `BulkDeleteItems` / `BulkAddItemTag` / `BulkTagResult` を提供。既存 `internal/store/store.go` / `tags.go`（後者は repo 上に存在しないため触らず、`TagInput` 型は `store.go` line 86-89 に定義済）は変更しない。
- 重要な判断:
  - `tags` テーブルへの INSERT は既存 `upsertItemTags`（store.go line 376-398）の慣用句を踏襲し `INSERT INTO tags (name, normalized_name) VALUES ($1, $2)` の 2 列指定（`id` は schema の DEFAULT `gen_random_uuid()` に委ねる）。tasks.md 本文は `INSERT INTO tags (id, name, normalized_name) VALUES (gen_random_uuid(), ...)` を例示していたが、既存 store 規約と差異が出ない同等な形にした（DB 動作・RETURNING 結果は同一）。
  - `SELECT ... FOR KEY SHARE` の所有確認では、scan を完了してから `rows.Close()` を呼ぶ pgx v5 の慣用パターンに従い、defer は使わず明示 close + Err() チェックで実装。tx 上の rows は `tx.Query` の後続クエリと衝突するため defer close では順序保証が脆い。
  - `BulkAddItemTag` の step 4 で `unnest($3::uuid[])` の引数を `ownedItemIDs`（step 1 の SELECT 結果）に絞ったので、items への FK 違反は構造上発生しない。step 1 の KEY SHARE ロックが commit までこれら row の存在を保証する。
  - `item_tags.display_name` は migration 006 で追加された per-user 表示名列。`BulkAddItemTag` の INSERT 文では `tagInput.Name`（normalize 前の表示名）を `display_name` に書き、`ON CONFLICT DO NOTHING` で既存行があれば書き換えないようにした（Req 5.4 重複なし、かつ既存表示名 / casing を保持）。これは既存 `CreateItem` の `upsertItemTags` が `ON CONFLICT DO UPDATE SET display_name=EXCLUDED.display_name` で更新する規約とは異なるが、本 task は「追加のみ（既存タグは保持）」という Req 5.3 / 5.4 の方針に合わせて `DO NOTHING` を採用（design.md の SQL 仕様と一致）。
- 残存課題: なし。task 2 で integration test が追加されると、SQL の正しさ（per-user 分離 / 部分失敗 / 重複防止 / 並行 DELETE での FK race 回避）が実 DB で固定される。

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
