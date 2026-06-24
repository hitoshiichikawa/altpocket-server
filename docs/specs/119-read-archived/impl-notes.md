# Implementation Notes (#119)

## Implementation Notes

### Task 1
- 採用方針: tasks.md task 1 の指示通り、`migrations/007_add_item_status.sql` を新規作成し、`ALTER TABLE items ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unread'` + `DO $$ EXCEPTION` ブロック包みの CHECK 制約 + `CREATE INDEX IF NOT EXISTS items_user_status_idx` の 3 ステートメント構成で実装した。
- 重要な判断:
  - backfill は ADD COLUMN ... NOT NULL DEFAULT 'unread' により自動成立するため、追加 UPDATE 文は導入しなかった（design.md "Migration Strategy" / Req 1.3 / 6.1 と整合。PostgreSQL 11+ の高速 default 最適化で既存行を書き換えずに NOT NULL を成立させる）。ファイル内コメントにこの判断を明記した。
  - CHECK 制約は task 1 の説明にある 2 案（`DO $$ EXCEPTION WHEN duplicate_object` / `pg_constraint` を `IF NOT EXISTS` で先読み）のうち、書式が短く 006 の冪等性パターンと整合する前者（`DO $$ BEGIN ... EXCEPTION WHEN duplicate_object THEN NULL; END $$;`）を採用した。
  - ファイル冒頭コメントは 006_item_tag_display_name.sql と同程度の粒度で「背景 / 適用順 / 冪等性パターン / backfill 戦略」の 4 ブロック構造に整理し、Issue #119 / design.md の参照点を明示した。
- 残存課題: なし（task 2 以降の store 層・server 層・MCP 層・SSR 実装が後続 task として残っているが、本 task の境界（migrations）には影響しない）。

### Task 2
- 採用方針: tasks.md task 2 の指示通り、`internal/store` の `Item.Status` フィールド追加・`ItemStatusUnread`/`Read`/`Archived` 定数公開・`UpdateItemStatus` 新規メソッド・`ListItems` および `ListRecentItems` の `statuses []string` 引数拡張・`GetItemDetail` の `SELECT i.status` 追加を **単一 commit** にまとめた。`json_tags_test.go` に `Status: "read"` 入力 → `"status"` snake_case JSON 出力の assert を追加し、`go test ./internal/store/...` が通る状態で停止（task 2 規約の "spec 内の単体差分のみ作成" を満たし、server / mcpserver / worker の compile error は task 4 / 5 / 後続で順次解消する）。
- 重要な判断:
  - **UpdateItemStatus の SQL pattern**: design.md / tasks.md task 2 に明示された data-modifying CTE（`WITH prev AS (... FOR UPDATE) UPDATE items SET status=$3 FROM prev WHERE items.id=prev.id RETURNING prev.status`）を素直に実装した。通常の `UPDATE ... RETURNING status` だと **更新後**の値が返ってしまうため NFR 3.1 の遷移前後ログを生成できない（design.md 設計判断 #6 と整合）。`FROM prev WHERE items.id = prev.id` で UPDATE を `prev` CTE に明示依存させ、`FOR UPDATE` ロック取得 → UPDATE の評価順を強制する。
  - **ErrNoRows collapse**: 行未存在と他ユーザー所有を区別せずに `pgx.QueryRow.Scan(...)` が `pgx.ErrNoRows` を返す経路で NFR 2.1（所有チェック失敗が "存在しない" と同様に観測されること）を満たす。`prev` CTE が空（行未存在 / 所有外）なら UPDATE 対象行ゼロとなり Scan が ErrNoRows を返す。
  - **store 層は default を持たない**: `ListItems(... statuses []string ...)` / `ListRecentItems(... statuses []string)` ともに `len(statuses) > 0` でのみ `i.status = ANY($N)` を WHERE に追加する。`nil` / 空 は「フィルタ無し（全状態）」として扱い、`unread` 既定 / `nil` 既定（後方互換）の判断は呼び出し側（task 4 の `parseStatusFilter(defaultIfEmpty)` / task 5 の `mcpStatusFilter`）の責務に委ねる。これにより `/v1/items` の Req 6.2（後方互換: 既定 nil = 全状態）と `/ui/items` の Req 3.1（既定 unread）を 1 つの store メソッドで両立できる（design.md "Store.ListItems / ListRecentItems（拡張）" 節と整合）。
  - **`ListRecentItems` の SQL 構築方式**: 既存 `ListItems` の `[]string{} + argPos` パターンを完全踏襲すると WHERE 句数が少なすぎて over-engineering になるため、`where` を `string` で開始し `fmt.Sprintf` で argPos を 1 箇所だけ補完する最小化形にした。同等の `i.status = ANY($N)` を AND 結合する点で `ListItems` と振る舞い一致。
  - **Item.Status の JSON タグ位置**: `Status string \`json:"status"\`` を `FetchError` と `CreatedAt` の間に配置（`FetchStatus` の次の "user-visible state" 軸として並ぶことで model の意図が読み取りやすい）。`json_tags_test.go` の既存 `assertMissingKey(t, m, "Status")` 追加で PascalCase 漏洩も regress-fix。
  - **dependent packages の compile error は意図的に放置**: `go build ./...` は server / mcpserver で 3 件の compile error を吐く（`s.store.ListItems` の引数不一致 / `*store.Store` が `mcpserver.DataSource` を満たさない）が、tasks.md task 2 の "本タスクではコンパイル成立は require しない" 注記通り。dependent fix は task 4 (server) / task 5 (mcpserver) で順次入る。`go test ./internal/store/...` は単独で pass する。
- 残存課題:
  - **lint 不在**: per-task Implementer 環境に `golangci-lint` が未 install のため、本タスクで追加した Go コードに対する lint は実行できなかった（task 1 と同じ状況）。gofmt は pass、`go vet ./internal/store/...` も pass を確認済み。後続 task で Go 変更が積まれる際に同 verify gate で lint が再実行されるため、本 commit 時点では問題なし。
  - **`UpdateItemStatus` の入力 enum 検証**: store 層では `next` 文字列を素通しで SQL に渡す（DB CHECK 制約が defense-in-depth）。入力検証は呼び出し側 `handleSetItemStatus`（task 4）の責務に置く設計（design.md "Store.UpdateItemStatus" Preconditions 節）。task 3 の `TestUpdateItemStatus_RejectsInvalidStatus` が CHECK 制約による拒否を実 DB で回帰検証する。
  - **`ListRecentItems` の DISTINCT 維持**: 既存実装は `array_agg(DISTINCT t.id) FILTER (...)` の DISTINCT を保っており、本変更では `i.status` 追加と `WHERE` 拡張以外は触っていない。`ListItems` 側の非 DISTINCT array_agg（`ORDER BY t.id` で安定化）と挙動差があるが本 Issue のスコープ外（既存 #115 等で議論された設計判断）。

### Task 3
- 採用方針: tasks.md task 3 の指示通り、`internal/store/store_item_status_test.go` を `//go:build integration` 付きで新規作成し、10 種類のテスト（`TransitionsAllPairs` / `RejectsOtherUserItem` / `RejectsInvalidStatus` / `TestListItems_FilterByStatus` / `TestListRecentItems_FilterByStatus` / `TestMigration007_BackfillsExistingItemsToUnread` / `TestCreateItem_DefaultsToUnread` / `TestUpdateItemStatus_DoesNotMutateFetchStatus` / `TestWorkerFetchUpdatesDoNotMutateStatus` / `TestWebUpdateReflectsInMCPListRecent`）を 1 ファイルにまとめた。既存 `mcp_api_key_test.go` の `newIntegrationStore` / `seedTestUser` ヘルパーを **同一パッケージ内で再利用**（package-local symbol、build tag も `integration` で一致）。`tags_lookup_test.go` のように別名ヘルパー（`newTagsLookupStore`）を作る選択肢もあったが、`mcp_api_key_test.go` 側のヘルパーは「最小限 / 副作用なし / `*Store` を返すだけ」のため再利用が無摩擦と判断した。
- 重要な判断:
  - **007 backfill テストの実 DB 戦略 (TestMigration007)**: tasks.md task 3 は `migrations/006_*.sql` までを apply した一時 schema を作成し 007 を後追いで apply する pattern を例示するが、CI / 開発者ローカルの共通 TEST_DATABASE_URL は **007 適用済み** が前提（既存 `items_active_filters_integration_test.go` の運用と同じ）。一時 schema の作成 / drop は実行時間と権限要件が膨らむため、`s.DB.Begin(ctx)` で **transaction を開き、その中で `DROP CONSTRAINT items_status_check` / `DROP COLUMN status` / `DROP INDEX items_user_status_idx` で pre-007 schema を再構築 → 既存風 items 行を seed → 007 SQL 3 ステートメントをそのまま流し直す → backfill を assert → tx.Rollback で破棄** という pattern を採った。PostgreSQL の transactional DDL によりこの全フローが他コネクションから不可視で、共有 TEST_DATABASE_URL を汚さない（design.md "Migration Strategy" 節と整合）。CHECK 制約 active の検証は SAVEPOINT で sub-tx を切り、`UPDATE ... SET status = 'bogus'` の失敗を捕捉 → SAVEPOINT へロールバックする標準パターン。
  - **2 軸独立性 (Req 1.6) の 2 方向検証**: design.md Req 1.6 / Architecture Integration の「fetch 軸と user 状態軸の独立」を **両方向**から固定した。`TestUpdateItemStatus_DoesNotMutateFetchStatus` は status 軸の更新が fetch_status 軸を巻き込まないことを 4 種類の seed `fetch_status` × 3 段遷移 で網羅、`TestWorkerFetchUpdatesDoNotMutateStatus` は worker 経路の `ClaimItemsForFetch` / `UpdateFetchSuccess` / `UpdateFetchFailure` が status を巻き込まないことを `read` / `archived` 状態の item で検証する。worker 側の code は本 task で変更しないが、store 関数の SET 句が status を含まないことを store integration test レイヤで lock することで、将来 worker SQL の改修で意図せず status が SET された場合の regression を捕まえられる。
  - **CHECK 制約違反の判定**: `TestUpdateItemStatus_RejectsInvalidStatus` で `s.UpdateItemStatus(..., "bogus_value")` が `pgx.ErrNoRows` ではなく非 nil error を返すことを assert する。`UpdateItemStatus` の SQL は `pgx.QueryRow.Scan` 経路のため、所有チェック失敗（行不在）と CHECK 制約違反（行はある / UPDATE が CHECK で reject）を **同じ Scan 経路** で観測する。両者を取り違えると Req 1.5（範囲外を拒否）と NFR 2.1（他ユーザー所有を拒否）の signal が混ざるため、`errors.Is(err, pgx.ErrNoRows)` を **逆方向ガード** として明示的に書き、CHECK violation を取り違えていないことを担保した。
  - **ListItems / ListRecentItems の filter 検証構造**: 同じ 5 ケース（nil / 空 / unread / unread+read / archived）を 2 ヘルパー関数 (`collectItemIDs` + `equalStringSlices`) で table-driven に書き、ID 集合比較で順序差を吸収。3 件 seed → 各フィルタで 1/2/3 件の期待が成立することを 1 つの test 関数内に集約することで、テスト数を増やさず Req 3.3 / 3.4 / 3.5 / 6.2 / 5.3 をカバーした。
  - **Web↔MCP 整合の二重 assert (TestWebUpdateReflectsInMCPListRecent)**: `UpdateItemStatus(... "read")` 後に `ListRecentItems(... nil)` と `ListRecentItems(... ["read"])` の **2 経路**で item が見えることを assert し、Resource 側既定（nil）と Tool 側明示フィルタ (`["read"]`) のどちらでも単一 DB ソースの整合性を pin した（design.md Req 5.4 Traceability / `recent-articles` Resource の status 引数取扱 節 と整合）。
- 残存課題:
  - **実 DB での実行は環境依存**: per-task Implementer 環境（DB を spin-up しない方針 / `.kiro/steering/structure.md` 準拠）では本テストは実行できない。`go test -tags=integration ./internal/store/...` の実行検証は Reviewer フェーズもしくは開発者ローカルでの確認に委ねる。`go vet -tags=integration ./internal/store/...` と `go test -tags=integration -run=^$ ./internal/store/...` でコンパイル成立は本 task 内で確認済み。
  - **`golangci-lint` 不在**: task 1 / 2 と同じく per-task Implementer 環境に `golangci-lint` が install されていないため、追加した Go コードに対する lint は実行できなかった。gofmt は pass。後続 task で Go 変更が積まれる際に verify gate で lint が再実行される前提で問題なし。
  - **`go test ./...`（非 integration）の compile error は task 2 由来の既知の状態**: `internal/server` / `cmd/api` で `s.store.ListItems` のシグネチャ不一致による build error が出るが、これは task 2 の learning にも記載されている既知の事象で、task 4 (server) / task 5 (mcpserver) で順次解消される。本 task は `internal/store` パッケージ内のテスト追加のみであり、当該 build error は私の変更とは無関係（`go test ./internal/store/...` 単独は pass を確認済み）。

## Verify 実行記録

- `go test ./...`: 全 package pass（cached + `internal/server` 4.012s）。SQL のみの追加のため Go コード側に影響無し。
- `golangci-lint run`: 本 per-task Implementer 実行環境に `golangci-lint` バイナリが未 install のため実行できず（`command not found`）。本 task は Go コード変更を伴わない SQL ファイル単独追加であり、lint 対象に新規 Go ファイルが含まれないため、既存 Go コードの lint 結果は変化しない（影響ゼロ）。後続 task で Go コードを追加する際に同 verify gate で lint が再実行されるため、本 task の commit 時点では問題なし。
- 実 DB への migration 適用は per-task Implementer 環境（DB を spin-up しない方針 / `.kiro/steering/structure.md` 準拠）では行わない。動作検証は task 3 の integration test（`TestMigration007_BackfillsExistingItemsToUnread`）と Reviewer フェーズの手動確認に委ねる。

## 受入基準カバレッジ（task 1 範囲）

本 task は migrations 境界の単独実装であり、対応する AC のテスト追加は task 3
（`internal/store/store_item_status_test.go` の `TestMigration007_BackfillsExistingItemsToUnread` /
`TestCreateItem_DefaultsToUnread` / `TestUpdateItemStatus_RejectsInvalidStatus`）でカバーされる
設計（tasks.md の `_Depends: 1, 2_` 規約）。task 1 単独では SQL ファイル追加のみで挙動変更は
DB 適用時にのみ顕在化するため、`_Requirements:_` 列挙 AC（1.1 / 1.2 / 1.3 / 1.5 / 6.1 / NFR 1.1 / NFR 1.2）の
対応テストは後続 task 3 / task 9（performance verification）で完結する（task 1 内では
**migration ファイルの存在と書式の正しさ自体が AC を成立させる前提条件**であり、test fixture
の追加なしに SQL ステートメントの構造をもって AC を満たす設計）。

| AC | 担保方法 | 後続 task |
|----|----------|-----------|
| Req 1.1（3 値状態を保持） | CHECK 制約 `items_status_check` で 3 値以外を DB レイヤで拒否 | task 3 `TestUpdateItemStatus_RejectsInvalidStatus` |
| Req 1.2（初期状態 unread） | `DEFAULT 'unread'` カラム定義 | task 3 `TestCreateItem_DefaultsToUnread` |
| Req 1.3（既存アイテム backfill） | `ADD COLUMN NOT NULL DEFAULT 'unread'` による自動 backfill | task 3 `TestMigration007_BackfillsExistingItemsToUnread` |
| Req 1.5（範囲外を拒否） | CHECK 制約 `items_status_check` | task 3 `TestUpdateItemStatus_RejectsInvalidStatus`、server task 4 `TestHandleSetItemStatusInvalidStatusReturns400` |
| Req 6.1（データ消失なし） | `ADD COLUMN NOT NULL DEFAULT 'unread'` の自動 backfill | task 3 `TestMigration007_BackfillsExistingItemsToUnread` |
| NFR 1.1（一覧表示パフォーマンス） | 複合 index `items_user_status_idx (user_id, status, created_at DESC)` | task 9 / Verify 節 "Performance verification" |
| NFR 1.2（タブ切替パフォーマンス） | 同上の複合 index | task 9 / Verify 節 "Performance verification" |

STATUS: complete
