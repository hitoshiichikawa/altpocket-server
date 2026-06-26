# Implementation Plan

本 spec のタスクは以下の依存順で実装する。Web UI が重い責務は「タブ SSR」「カードボタン
SSR」「JS 状態切替」「JS タブ切替」を別タスクに分割し、各タスクが `DEV_MAX_TURNS=60` 以内に
収まるようにした。

- [x] 1. マイグレーション 007: items.status カラム追加と backfill
  - `migrations/007_add_item_status.sql` を新規作成
  - `ALTER TABLE items ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unread'`
  - **CHECK 制約の冪等追加**: PostgreSQL 16 には `ADD CONSTRAINT IF NOT EXISTS` が存在しない
    ため、`DO $$ BEGIN ... EXCEPTION WHEN duplicate_object THEN NULL; END $$;` ブロックで
    `ALTER TABLE items ADD CONSTRAINT items_status_check CHECK (status IN ('unread', 'read', 'archived'))`
    を包む。または `pg_constraint` を `IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE
    conname = 'items_status_check' AND conrelid = 'items'::regclass)` で先読みする plpgsql
    ブロックでも可。いずれも再実行時に `duplicate_object` を吸収して冪等を成立させる
    （Reviewer 指摘 #3 反映）
  - `CREATE INDEX IF NOT EXISTS items_user_status_idx ON items (user_id, status, created_at DESC)`
  - ファイル冒頭コメントに Issue #119 の意図・background・既存マイグレーション 001..006 との
    相対適用順、および冪等性パターン（`IF NOT EXISTS` + `DO $$ EXCEPTION` ブロック）の趣旨を記述
  - 既存マイグレーション（001〜006）の中身は **書き換えない**
  - _Requirements: 1.1, 1.2, 1.3, 1.5, 6.1, NFR 1.1, NFR 1.2_
  - _Boundary: migrations_
  - 注: `items_user_status_idx` は design.md Requirements Traceability で NFR 1.1 / NFR 1.2 の
    パフォーマンス対策の主担当として紐付くため、index 作成タスクである本タスクの
    `_Requirements:_` にも NFR 1.1 / NFR 1.2 を明示する（Reviewer r6 指摘 #6 反映）

- [x] 2. store 層: Item.Status / 状態定数 / UpdateItemStatus / ListItems 拡張
  - `internal/store/store.go`:
    - `Item` 構造体に `Status string \`json:"status"\`` を追加
    - 定数 `ItemStatusUnread = "unread"` / `ItemStatusRead = "read"` /
      `ItemStatusArchived = "archived"` を package-level で公開
    - 新規メソッド `UpdateItemStatus(ctx, userID, itemID, next string) (prev string, err error)`
      を実装（所有チェック + UPDATE + 旧 status 取得 / `pgx.ErrNoRows` で 404 collapse）
      - **旧 status 取得の SQL pattern**: 通常の `UPDATE ... RETURNING status` は **更新後**
        の値を返してしまうため、NFR 3.1（遷移前後ログ）には CTE を用いて更新前行を捕捉する。
        実装例（**UPDATE が `prev` CTE に依存する** 形にして、`FOR UPDATE` で行を取った後に
        必ず UPDATE が走るよう順序を強制する。PostgreSQL の data-modifying CTE では同一ステートメント
        内の sub-CTE 間の評価順は依存関係でのみ確定するため、依存無しで並べると `FOR UPDATE` の
        ロック取得前に `UPDATE` 側が再 SELECT してしまい `prev` の値が不正確になる可能性がある /
        設計 #6）:
        ```sql
        WITH prev AS (
          SELECT id, status FROM items
          WHERE id = $1 AND user_id = $2
          FOR UPDATE
        )
        UPDATE items
        SET status = $3
        FROM prev
        WHERE items.id = prev.id
        RETURNING prev.status;
        ```
      - 上記 1 クエリで「行が存在しない / 他ユーザー所有」を `pgx.ErrNoRows` として
        collapse でき、`prev.status` は更新前 status を返す（PostgreSQL 16 で動作）
      - `FROM prev WHERE items.id = prev.id` で UPDATE を `prev` CTE に明示的に依存させることで、
        `FOR UPDATE` によるロック取得 → UPDATE 実行の順序を保証する。`prev` が空（行未存在）の
        場合は UPDATE 対象行もゼロとなり `pgx.QueryRow` が `pgx.ErrNoRows` を返す
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
    本タスクで完結させる（Req 1.1 に対する同 task 内テスト必須）
  - _Requirements: 1.1, 1.4, 1.6, 3.3, 3.4, 3.5, 6.2, NFR 2.1, NFR 3.1_
  - _Boundary: Store_

- [x] 3. store 層 integration test: UpdateItemStatus / ListItems status フィルタ / 007 backfill / 2 軸独立性 / Web↔MCP 整合
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
    - **`TestMigration007_BackfillsExistingItemsToUnread`** (Req 1.3 / 6.1 backfill 回帰):
      007 migration 適用 **前** のスキーマ snapshot に対して既存 items を複数件作成し、
      その後 007 migration を実 DB に適用 → 全行が `status='unread'` になっていることと、
      backfill 後に CHECK 制約が active であることを assert（design.md:743 の計画を task に
      落とし込む / Reviewer 指摘 #4）。前段スキーマの再現は `migrations/006_*.sql` までを
      apply した一時 schema を作成し、`migrations/007_*.sql` を後追い適用する pattern で行う
    - **`TestCreateItem_DefaultsToUnread`** (Req 1.2 新規 item 既定):
      `Store.CreateItem`（または同等の新規 item 作成経路）を呼び、status カラムを明示指定
      しないとき、永続化された行が `status='unread'` となることを実 DB で assert する
      （DEFAULT 'unread' が新規行に効くことを回帰固定 / Reviewer 指摘 #4）
    - **`TestUpdateItemStatus_DoesNotMutateFetchStatus`** (Req 1.6 / 2 軸独立性):
      `fetch_status='success'` / `'failed'` / `'pending'` / `'fetching'` の各 fetch_status を
      持つ item を seed → `UpdateItemStatus` で `unread → read → archived → unread` の
      全遷移を実行 → 各遷移後に `fetch_status` の値が seed 時点から **不変** であることを
      assert する（status 軸の更新が fetch_status 軸を巻き込まないことを実 DB で回帰固定）
    - **`TestWorkerFetchUpdatesDoNotMutateStatus`** (Req 1.6 / 2 軸独立性):
      `status='read'` / `'archived'` の item を seed → 既存 `ClaimItemsForFetch` /
      `UpdateFetchSuccess` / `UpdateFetchFailure`（`cmd/worker` 経路と同一の store 関数）を
      順次呼び出し → 呼び出し後に `status` カラムが seed 時点から **不変** であることを
      assert する（fetch 軸の更新が user status 軸を巻き込まないことを実 DB で回帰固定）。
      worker 側コード（`cmd/worker`）の挙動変更は本タスクで行わないが、worker が呼ぶ
      store 関数の SET 句が `status` を含まないことを **store integration test レイヤで**
      ロックする
    - **`TestWebUpdateReflectsInMCPListRecent`** (Req 5.4 Web↔MCP 整合):
      実 DB に item を seed → `UpdateItemStatus(userID, itemID, "read")` を呼び出して
      永続化 → 後続で `Store.ListRecentItems(userID, since, nil)`（MCP の
      `recent-articles` が呼ぶ store 関数と同一）を呼んだとき、返却された item の
      `Status` が `"read"` に更新済みであることを assert する。`statuses=nil` /
      `statuses=["read"]` の 2 ケースで実 DB を介した一貫性を回帰固定する
      （Reviewer 指摘 #8 反映: PATCH/update → MCP read updated status の end-to-end を
      store integration test レイヤで担保し、Web UI ↔ MCP の単一 DB ソース整合を検証）
  - 既存 `items_active_filters_integration_test.go` の `newIntegrationStore` パターンと
    `seedItemsActiveFilterUser` パターンを参考にし、cleanup 規約に従う
  - _Requirements: 1.2, 1.3, 1.4, 1.5, 1.6, 3.3, 3.4, 3.5, 5.4, 6.1, 6.2, NFR 2.1_
  - _Boundary: Store_
  - _Depends: 1, 2_

- [x] 4. server 層: handleSetItemStatus / parseStatusFilter / handleListItems / handleUIItems 接続
  - `internal/server/server.go`:
    - `parseStatusFilter(q url.Values, defaultIfEmpty []string) []string` を追加（design.md の
      表通り。第 2 引数で「`?status=` 不在 / 空 / 不明値 のときに返す既定値」を呼び出し側が指定する。
      マッピングは `unread` / `all` / `archived` の **3 値のみ**を parser 内で固定し、`read`
      単独入力（UI タブに対応しない値）と他の不明値は `defaultIfEmpty` にフォールバックする
      / Reviewer 指摘・design.md「parseStatusFilter」節）
    - `handleSetItemStatus(w, r)` を追加: JSON `{"status":"<v>"}` を受理、enum 検証、`requireAuth`
      / `limiter` / CSRF は既存 middleware 経由、`Store.UpdateItemStatus` 呼び出し、成功時
      `slog.Info("items.status.update", user_id, item_id, prev, next, request_id)` を出力。
      `pgx.ErrNoRows` は 404 `{"error":"not_found"}` に collapse（存在しない / 他ユーザー所有
      を区別せず NFR 2.1 でリーク防止）
    - `route("/v1/items", ...)` 配下に `r.Patch("/{id}/status", s.requireAuth(s.handleSetItemStatus))` を追加
    - `handleListItems`: `statuses := parseStatusFilter(r.URL.Query(), nil)` を渡す（Req 6.2
      後方互換: 既存 `/v1/items` クライアントは status 未送信で全状態を取得し続ける）
    - `handleUIItems`: `statuses := parseStatusFilter(r.URL.Query(), []string{store.ItemStatusUnread})`
      を渡す（Req 3.1: Web UI 初期表示で unread のみ）
    - `handleUIItems` のテンプレート data に `"StatusTab"` / `"StatusTabURLs"` / **`"StatusQuery"`**
      を追加（次タスクでテンプレート側を実装）。`StatusQuery` は現在 URL の `?status=` 生値
      （未指定時は空文字）で、検索 form / sort form / per-page form の hidden input に注入する
    - 既存の **clear filters / sort / per-page link builder** が `?status=` を温存することを確認し、
      温存していない場合は server 側のヘルパー（`buildActiveTagFilters` 周辺の URL ビルダー）に
      `?status=` の引き継ぎを追加する（Req 3.6 の併用、設計 #5）
  - `internal/server/items_status_test.go` を新規作成（テスト計画 / Req カバレッジ対応表）:
    - `Test_parseStatusFilter_TableDriven`: `""` / `"unread"` / `"all"` / `"archived"` /
      `"read"`（**UI タブに対応しない値、defaultIfEmpty にフォールバックすることを assert**） /
      不明値 / 大文字混在 を、`defaultIfEmpty=nil` と `defaultIfEmpty=["unread"]` の 2 系列で
      table-driven テスト（Req 3.1, 3.3, 3.4, 3.5, 6.2 をカバー、`"read"` 単独受理が UI タブの
      第 4 状態を生まないことを回帰固定）
    - `TestHandleSetItemStatusUnauthorizedReturnsJSONError`: requireAuth 未通過時 401 JSON（既存契約維持）
    - `TestHandleSetItemStatusInvalidJSONReturns400`: parse 不能 JSON → 400 invalid_request
    - `TestHandleSetItemStatusEmptyStatusReturns400`: `{}` または `{"status":""}` → 400 invalid_request
    - `TestHandleSetItemStatusInvalidStatusReturns400`: `{"status":"foo"}` → 400 invalid_status（Req 1.5）
    - `TestHandleSetItemStatusSuccessReturns200` (**integration tag**): 正常遷移時 200 +
      `{"status":"<next>","item_id":"<id>"}` レスポンス本文を assert する（Req 1.4 / 2.3〜2.6 の
      **server 側 JSON API 契約**をカバー）。`handleSetItemStatus` は JSON API であり、
      カード DOM の `data-status` 属性更新は `static/app.js` の責務（タスク 8 の
      `static/items_status.test.mjs` でカバー）であるため、本 server テストでは DOM 更新には
      触れない。**実装上の都合**: 現行 `Server` は concrete `*store.Store` フィールドを持ち
      store 差し替え用 interface を持たない（本 Issue では interface 化を scope 外とする）ため、
      成功応答経路は実 DB 経由でしか到達できない。よって本テストは `//go:build integration`
      tag 付きとし、`go test -tags=integration ./internal/server/...` で実行する
      （NotFound / OtherUser の 2 件と同じ build tag / DB セットアップを共有。Verify 節
      「Integration test の取扱」を参照 / Reviewer r6 指摘 #3 反映）
    - `TestHandleSetItemStatusNotFoundForMissingID` (integration tag): **実 DB を用いる
      integration test**。存在しない `item_id`（UUID 形式は valid だが DB 行が無い）に対する
      PATCH で 404 not_found が返ることを assert する（`pgx.ErrNoRows` の collapse 経路を
      実 store 経由で回帰検証）
    - `TestHandleSetItemStatusOtherUserItemReturns404` (integration tag): **実 DB を用いる
      integration test**。user A の item を seed → user B として PATCH 試行 → 404 not_found
      が返り、対象行の status が seed 時点から不変であることを assert する（NFR 2.1 の
      サーバ層拒否を回帰検証）
    - **設計判断 / Reviewer 指摘 #5 反映 (r0) + r6 拡張**: 当初は上記 NotFound / OtherUser の
      **2 件のみ**を「fake / mock の store で `pgx.ErrNoRows` をシミュレートする」設計だったが、
      現行 `Server` は concrete な `*store.Store` フィールドを持ち（`internal/server` には
      store 差し替え用 interface が無い）、本 Issue では store interface 化の責務拡張は scope
      外とする。代わりに altpocket の既存 pattern（`items_active_filters_integration_test.go`
      等）と同じく `//go:build integration` tag 付きで実 DB を介して当該 4 件
      （**SuccessReturns200 / LogsTransitionFields / NotFoundForMissingID / OtherUserItemReturns404**）
      を検証する。**r6 拡張点**: 当初は 200 経路（SuccessReturns200）と log 検証
      （LogsTransitionFields）を通常 server テスト（`go test ./...` 経路）で書ける想定だったが、
      success 経路は `UpdateItemStatus` の `prev` 返却が実 DB に依存するため fake では成立せず、
      log 検証も同経路を通る必要があるため、両者も integration tag 側に移した
      （Reviewer r6 指摘 #3）。本 4 件は task 3 の integration test と同じ build tag /
      DB セットアップを共有するため、Verify 節の `go test ./...` には含まれず、
      `go test -tags=integration ./internal/server/...` で実行される（Verify 節
      「Integration test の取扱」を参照）。一方 `go test ./...` 経路では 400 系
      （InvalidJSON / EmptyStatus / InvalidStatus）/ 401 系（Unauthorized）/
      parser（`Test_parseStatusFilter_TableDriven`）/ `handleUIItems` データ整合
      （`TestHandleUIItemsTemplateDataIncludesStatusQuery`）が引き続き実行され、Req 1.5 /
      3.1 / 3.3〜3.6 / 3.8 / 6.2 のバリデーション・データ整合 AC は通常テスト経路で担保される
    - `TestHandleSetItemStatusLogsTransitionFields` (**integration tag**): 成功時の
      `slog.Info("items.status.update", ...)` に `user_id` / `item_id` / `prev` / `next` の
      4 フィールドが正しい値で含まれ、かつ Cookie / session token / Authorization ヘッダ /
      本文の生値が出力**されない**ことを assert する（`slog` の handler を test 用 buffer に
      差し替えて JSON line を観察。NFR 3.1 の機密非出力と遷移ログの両方を回帰検証）。
      **build tag の根拠**: 成功遷移ログは `UpdateItemStatus` が `prev` 値を実 DB から返す
      経路に依存し、`*store.Store` を差し替え不能な現行構造下では実 DB 経由でしか発火しない。
      上記 `TestHandleSetItemStatusSuccessReturns200` と同じく `//go:build integration` tag
      付きとし、`go test -tags=integration ./internal/server/...` で実行する
      （Reviewer r6 指摘 #3 反映 / 設計 #9）
    - `TestHandleUIItemsTemplateDataIncludesStatusQuery`: `handleUIItems` が `?status=` を
      `StatusQuery` テンプレート data として渡し、検索 form / sort form の hidden input ビルダー
      および clear-filters / pagination URL ビルダーが `?status=` を温存することを assert
      （Req 3.6 / 3.8 の併用永続を回帰検証 / 設計 #5）
  - `extension_contract_test.go` は **変更しない**（成功時の JSON フィールド構造は assert
    していないため）
  - **テスト追加（同 task 内）**: 上記 10 種の handler / parser テストを本タスクで完結させる
    （Req 1.4 / 1.5 / 2.3〜2.6 / 3.1 / 3.3〜3.6 / 3.8 / 6.2 / NFR 2.1 / NFR 3.1 の同 task 内テスト必須カテゴリに該当）。
    内訳の実行経路:
    - 通常 `go test ./...` 経路（6 件）: `Test_parseStatusFilter_TableDriven` /
      `TestHandleSetItemStatusUnauthorizedReturnsJSONError` / `TestHandleSetItemStatusInvalidJSONReturns400` /
      `TestHandleSetItemStatusEmptyStatusReturns400` / `TestHandleSetItemStatusInvalidStatusReturns400` /
      `TestHandleUIItemsTemplateDataIncludesStatusQuery`
    - integration tag 経路（4 件、`go test -tags=integration ./internal/server/...`）:
      `TestHandleSetItemStatusSuccessReturns200` / `TestHandleSetItemStatusLogsTransitionFields` /
      `TestHandleSetItemStatusNotFoundForMissingID` / `TestHandleSetItemStatusOtherUserItemReturns404`
    - **AC traceability に対する watcher / per-task Reviewer の判定**: 通常経路で実行される
      400 / 401 / parser / handleUIItems data 整合のみで Req 1.5 / 3.1 / 3.3〜3.6 / 3.8 / 6.2 /
      NFR 2.1（401）を満たすが、Req 1.4 / 2.3〜2.6（成功遷移）/ NFR 2.1（404 経路）/ NFR 3.1
      （構造化ログ）は integration tag 経路に依存するため、これらの AC については本タスク内に
      **integration test を含めた上で deferred test ではない**ものとして扱う（per-task Reviewer
      ループ運用時の `_Requirements_partial:_` 宣言は不要 / .claude/rules/tasks-generation.md
      「task-test 境界整合の規約」参照）
  - _Requirements: 1.4, 1.5, 1.6, 2.3, 2.4, 2.5, 2.6, 3.1, 3.3, 3.4, 3.5, 3.6, 3.8, 6.2, NFR 2.1, NFR 3.1_
  - _Boundary: Server_
  - _Depends: 2_

- [x] 5. mcpserver 層: status 引数 / status 出力フィールド / DataSource 拡張
  - `internal/mcpserver/deps.go`: `DataSource.ListItems` / `ListRecentItems` のシグネチャを
    store の新シグネチャに揃える（`statuses []string` 追加）
  - `internal/mcpserver/server.go`:
    - `ListItemsInput` / `SearchItemsInput` に `Status string \`json:"status,omitempty"\`` を追加
    - 内部ヘルパー `mcpStatusFilter(s string) []string` を追加（**既定 `nil`（全状態 / Req 6.3
      後方互換）**、`unread`/`read`/`archived`/`all` を受理、不明値・複数指定は `nil`
      フォールバック / design.md「既定値・受付値の確定」参照）
    - `listItemsHandler` / `searchItemsHandler` で `mcpStatusFilter(args.Status)` の結果を
      store に渡す
    - `recentArticlesHandler` は **MCP Resource**（`*mcp.ReadResourceRequest` を受け取り、
      `ListItemsInput` / `SearchItemsInput` のような構造化 input 引数を持たない）であるため、
      `args.Status` は存在しない。`ListRecentItems` 呼び出しでは固定で `nil`（全状態）を渡す。
      Req 5.3（クライアントが状態を引数で指定）は input 引数を持つ Tool 側（list_items /
      search_items）で満たし、Req 5.2（既定値を本仕様内の 1 値に固定）は recent-articles では
      `nil`（全状態）で満たす（design.md「`recent-articles` Resource の status 引数取扱」節を参照）
    - `formatItemList` / `getItemHandler` の出力 JSON に `"status": item.Status` / `"status": detail.Status` を追加
  - `internal/mcpserver/server_test.go`（テスト計画 / Req カバレッジ対応表）:
    - fake DataSource を新シグネチャに更新（`statuses []string` をキャプチャできる recorder 化）
    - `TestListItemsHandler_DefaultStatusIsNilAllStates`: `Status` 空入力で store に `nil`（全状態）
      が渡ることを assert（Req 5.2 / Req 6.3 後方互換）
    - `TestListItemsHandler_UnreadReturnsUnreadOnly`: `Status: "unread"` → `[unread]`（Req 5.3）
    - `TestListItemsHandler_ReadReturnsReadOnly`: `Status: "read"` → `[read]`（Req 5.3）
    - `TestListItemsHandler_ArchivedReturnsArchivedOnly`: `Status: "archived"` → `[archived]`（Req 5.3）
    - `TestListItemsHandler_AllReturnsUnreadAndRead`: `Status: "all"` → `[unread,read]`（Req 5.3 / 3.4 と統一）
    - `TestListItemsHandler_InvalidStatusFallsBackToNil`: `Status: "foo"` → `nil`（Req 6.3 破壊しない）
    - `TestListItemsHandler_MultiValueStatusFallsBackToNil` (**error mode B / handler-level fallback**):
      区切り文字埋め込みの複数指定が `nil` にフォールバックすることを assert する（Req 5.3 の
      「複数指定は既定にフォールバック」、design.md「複数指定の取扱」error mode (B) /
      Reviewer 指摘）。`Status: "unread,read"`（カンマ区切り） / `Status: "unread read"`
      （スペース区切り） / `Status: "unread,read,archived"`（3 値カンマ区切り） /
      `Status: "unread|read"`（縦棒区切り）の各ケースで store に `nil`（全状態）が渡ることを
      表形式で assert。これにより design.md の `mcpStatusFilter` が canonical 値集合との
      完全一致のみで分岐し、それ以外は一律 `nil` に落とす規約（split 実装を導入しない
      設計判断）が回帰検証される
    - `TestListItemsHandler_RejectsNonStringStatusAtSchemaLayer` (**error mode A / schema-level
      reject**, Reviewer r6 指摘 #1 反映): JSON 配列・繰り返しキー・非文字列型の入力が MCP の
      JSON Schema 検証で **handler 到達前**に拒否され、tool call error 経路で client に返ることを
      assert する。具体的には MCP SDK 経由で以下の生 payload を擬似発射:
      - `{"status": ["unread","read"]}`（JSON 配列）
      - `{"status": 1}` / `{"status": true}` / `{"status": null}`（非文字列型）
      - URL クエリ繰り返しキー `?status=unread&status=read`（適用される場合のみ）
      これらに対して MCP SDK の validation error が返り、`mcpStatusFilter` および `listItemsHandler`
      の handler body が **呼ばれない**ことを assert（fake DataSource の `ListItems` call recorder が
      呼び出しゼロを記録）。design.md「複数指定の取扱」error mode (A) の「`mcpStatusFilter` は
      呼び出されない」性質を回帰固定する。実装上 MCP SDK の `mcp.CallToolRequest` 検証経路を
      test fixture から起動できない場合は、本 case をテスト計画上 `// TODO: covered by SDK
      e2e if reachable` として明示し、(B) の handler-level fallback テストのみで実装上の coverage
      を担保する（SDK 経路への直接 hook が困難な場合の **fallback 方針** / Reviewer r6 指摘 #1
      の「test plan 上の明示」優先）
    - `TestListItemsHandler_OutputContainsStatus`: 返却 JSON に `"status"` キーが含まれ、各 item の
      `status` 値が正確に出力されることを assert（Req 5.1）
    - `TestSearchItemsHandler_StatusPropagation`: `Status: "unread"` / `"read"` / `"archived"` / `"all"` /
      空 / 不明値 / **複数指定** (`"unread,read"` 等の区切り文字埋め込み) を入力に、store に正しい
      statuses が渡ることを assert（Req 5.3 / 6.3、search_items が list_items と同一のマッピングで
      動作することを回帰検証 / 複数指定が `nil` にフォールバックすることも同表に含める）
    - `TestSearchItemsHandler_OutputContainsStatus`: 検索結果 JSON に `"status"` キーが含まれることを assert（Req 5.1）
    - `TestGetItemHandler_OutputContainsStatus`: 単体 item 取得結果 JSON に `"status"` キーが含まれ、
      `getItemHandler` の出力にも status が露出することを assert（Req 5.1）
    - `TestRecentArticlesHandler_AlwaysCallsStoreWithNilStatuses`: `recent-articles` は MCP
      **Resource**（`*mcp.ReadResourceRequest` を受け取り、`ListItemsInput` / `SearchItemsInput`
      のような構造化 input 引数を持たない）であるため、ハンドラ呼び出しの種類によらず常に
      `ListRecentItems` の `statuses` 引数が `nil`（= 全状態 / status フィルタを WHERE に追加
      しない）であることを assert する（Req 5.2 の固定既定値が `nil` であることの回帰検証 /
      Reviewer 指摘・design.md「`recent-articles` Resource の status 引数取扱」節を参照）。
      `Status` 空入力経路や `Status: "unread"` 引数伝播経路は本ハンドラに存在しないため、
      対応する旧テストは削除する。Tool 側の status 引数経路の回帰は本タスク内の
      `TestListItemsHandler_*` / `TestSearchItemsHandler_*` で個別にカバーする
  - **テスト追加（同 task 内）**: 上記 12 種の MCP handler テストを本タスクで完結させる
    （Req 5.1 / 5.2 / 5.3 / 6.3 の同 task 内テスト必須）。複数指定フォールバック
    （`TestListItemsHandler_MultiValueStatusFallsBackToNil`）は Req 5.3 の独立 AC として
    `TestListItemsHandler_InvalidStatusFallsBackToNil`（単一不明値）と別ケースで明示し、
    `TestSearchItemsHandler_StatusPropagation` の表形式にも複数指定行を含めることで
    `list_items` / `search_items` 両 Tool で同一の `nil` 帰着を回帰検証する。
    `TestRecentArticlesHandler_*` は Resource が input 引数を持たない設計の回帰検証 1 件のみ
    （`AlwaysCallsStoreWithNilStatuses`）で完結し、`Status` 引数経路の回帰は Tool 側
    `TestListItemsHandler_*` / `TestSearchItemsHandler_*` で個別カバーする
  - _Requirements: 5.1, 5.2, 5.3, 5.4, 6.3, NFR 2.2_
  - _Boundary: McpServer_
  - _Depends: 2_

- [x] 6. SSR テンプレート: 状態タブ markup + items_list の data-status / status-badge + 既存フォームに status hidden 保持
  - `templates/items.html`:
    - 検索バー直下、`<section class="split">` の手前に `<nav class="status-tabs" role="tablist"
      aria-label="アイテム状態">` を追加。Unread / All / Archived の 3 タブを `<a role="tab"
      aria-selected="..." href="{{index .StatusTabURLs "unread"}}">` 形式で描画
    - active タブには `class="is-active"` と `aria-selected="true"` を付与（`{{if eq .StatusTab "unread"}}...{{end}}`）
    - **`?status=` を併用 UI で保持する hidden input / URL 構築（Req 3.6 / 3.8 / 設計 #5）**:
      既存 `templates/items.html` には GET form が **3 箇所**、`?status=` を捨ててしまう
      hard-coded `/ui/items` リンクが **2 箇所**あるため、以下を **全て** 修正対象として
      明示する（Reviewer 指摘 #6 反映）:
      - (1) `items.html:9` の mobile 上部検索バー `<form class="search-bar" ... method="get"
        action="/ui/items">` に `<input type="hidden" name="status" value="{{.StatusQuery}}">`
        を追加
      - (2) `items.html:24` のデスクトップ `<form id="filter-form" method="get"
        action="/ui/items" class="search-form">`（Search / Sort / Per Page / Tags / Apply
        を含む）に同じ hidden を追加
      - (3) `items.html:81` の mobile bottom sheet `<form method="get" action="/ui/items"
        class="search-form">`（Search / Sort by / Per Page / Tags / Apply を含む）に同じ
        hidden を追加
      - (4) `items.html:59` の `<a class="btn-tertiary tag-clear-btn" href="/ui/items">Clear
        filters</a>` を `href="/ui/items{{if .StatusQuery}}?status={{.StatusQuery}}{{end}}"`
        相当に書き換える（または server.go 側で `ClearFiltersURL` を data として注入し、
        テンプレートはそのプリビルド URL を参照する）
      - (5) `items.html:120` の mobile bottom sheet 内 `<a class="btn-tertiary"
        href="/ui/items" ...>Clear all</a>` も (4) と同じ書式に揃える（同 `ClearFiltersURL`
        data を再利用してよい）
      - `StatusQuery` 値が空文字（`?status=` 未指定）の場合は、hidden input を出力しても
        `?status=` を空値で付与しても、URL の意味論に影響しない（`parseStatusFilter` が
        `""` を `defaultIfEmpty` にフォールバックする / Req 3.1）。テンプレートの記述簡略化の
        ため hidden は条件分岐なしで出力してよい
      - **ページネーション link（`items_list.html:84` `PrevURL` / `items_list.html:88`
        `NextURL`）** はサーバ側ビルダー（`server.go` の pagination ヘルパー）が既存 URL
        クエリ全体を引き継ぐ pattern であれば追加作業不要だが、引き継いでいない場合は
        タスク 4 の `handleUIItems` 拡張で `?status=` 温存を追加する（実装時に必ず確認）
  - `templates/items_list.html`:
    - `<article class="tile item-card {{if eq .FetchStatus "failed"}}failed{{end}}"
      data-status="{{.Status}}" ...>` のように `data-status` を追加
    - meta 行に `<span class="item-status-badge" data-status="{{.Status}}" role="status"
      aria-label="状態: {{.Status}}">{{.Status}}</span>` を追加（Req 4.4: テキストラベル併用）
    - **詳細リンクの `?status=` 温存（Req 3.8 詳細往復で初期値に戻さない / Reviewer 指摘 #2）**:
      `items_list.html:50` の `<a class="tile-link" href="/ui/items/{{.ID}}">` を
      `href="/ui/items/{{.ID}}{{if $.StatusQuery}}?status={{$.StatusQuery}}{{end}}"`
      相当に書き換える（または server.go 側で各 item に `DetailURL` フィールドを構築済みで
      渡す）。これにより詳細から `/ui/items/<id>` 経由で戻った際、戻り URL に `?status=`
      が引き継がれる
    - ページネーション link の `href` ビルダーも `?status=` を温存することを確認（既存
      pagination URL ビルダーがクエリ全体を引き継ぐ pattern なら追加作業不要）
  - SSR でタブの aria-selected と URL クエリの整合性を取れることをハンドラ側 data 渡しで確認
    （server タスク 4 の `StatusTabURLs` / `StatusTab` / `StatusQuery` data 整備済み前提）
  - JS 無効環境でもタブが `<a href>` でフルページ遷移として動作することを目視確認
  - **テスト追加（同 task 内）**: テンプレート差分の単体テストは Go 側の既存 renderer test の
    枠を使わず、次タスク 7 のボタン追加・タスク 9 のスタイル追加と合わせた目視確認に統一する
    （SSR テンプレートのみの単独 regression test は本リポジトリで歴史的に低価値のため省略）。
    `?status=` 温存の挙動はタスク 4 の `handleUIItems` テスト（後述）で hidden input / URL
    builder 経由の挙動を回帰検証する
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 4.1, 4.4, NFR 4.2_
  - _Boundary: Templates_
  - _Depends: 4_
  - 注: 本タスクは `templates/items.html` の status-tabs markup を実装する。NFR 4.2 は「既読/
    アーカイブ操作要素**および**状態タブ」両系統に読み上げ可能テキストを要求するため、
    status-tabs 側の `aria-selected` / `aria-label="アイテム状態"` / リンクテキスト
    （Unread / All / Archived）が本タスクの責務になる（Reviewer r6 指摘 #5 反映、
    操作ボタン側の aria-label はタスク 7 でカバー）

- [x] 7. SSR テンプレート: item-card の既読/アーカイブボタン追加（archive 解除も含む）
  - `templates/items_list.html` の `.item-actions` 内に以下を追加（**`unread` を主軸に分岐**し、
    `read` / `archived` を「未読に戻す」側に集約する。JS の `next = currentStatus === 'unread' ? 'read' : 'unread'`
    と一致させる。Req 2.3 / 2.4 / 2.6 の整合）:
    - `<button type="button" class="btn-secondary mark-read-toggle" data-item-id="{{.ID}}"
      data-current-status="{{.Status}}" aria-label="{{if eq .Status "unread"}}既読にする{{else}}未読に戻す{{end}}">{{if eq .Status "unread"}}Mark read{{else}}Mark unread{{end}}</button>`
    - `<button type="button" class="btn-secondary archive-toggle" data-item-id="{{.ID}}"
      data-current-status="{{.Status}}" aria-label="{{if eq .Status "archived"}}アーカイブ解除{{else}}アーカイブする{{end}}">{{if eq .Status "archived"}}Unarchive{{else}}Archive{{end}}</button>`
  - `templates/item_detail.html` の actions 列にも同じ 2 ボタンを追加（任意・同 PATCH 経路を共有）
  - **詳細ページから一覧へ戻るリンクの `?status=` 温存（Req 3.8 詳細→Library 戻りで初期値に
    戻さない / Reviewer 指摘 #2）**: `item_detail.html:3` の
    `<a class="detail-back" href="/ui/items">&#x2190; Library</a>` を、ハンドラ
    （`handleUIItemDetail` 相当）が渡す `LibraryURL` テンプレート data（例: `/ui/items` 単独
    または `/ui/items?status=archived` 等の `?status=` を含む URL）を参照する形に書き換える。
    詳細ページに到達した際の Referer / 直前 URL から `?status=` を引き継ぐか、
    詳細ページ専用クエリ（`?from_status=archived`）を別途持つかは server 側で決める（推奨は
    詳細ページ URL に `?status=` を伝播させる方針。`items_list.html` の tile-link 修正と
    一貫させる）。これにより Archived / All 一覧 → 詳細 → Library 戻りで Unread に
    強制リセットされる挙動を防ぐ
  - Tab フォーカス順序が既存ボタン（Original / Refetch / Delete）と整合することを目視確認
  - **テスト追加（同 task 内）**: テンプレートのみの単独 Go test は既存規約上追加せず、次タスク
    8 の JS テストと組み合わせて statictest（`extension/sidepanel.test.mjs` と同じ
    `node --test` 系）でカバーする方針を本タスク内で明示。本タスクは markup 追加のみ
  - _Requirements: 2.1, 2.2, 2.6, NFR 4.1, NFR 4.2_
  - _Boundary: Templates_
  - _Depends: 6_

- [x] 8. static JS: 状態切替ボタンの delegated click + 失敗時巻き戻し
  - `static/app.js` の既存 delegated click handler（refetch / delete の隣）に追加:
    - `button.mark-read-toggle`: `currentStatus` を **`btn.dataset.currentStatus`** から読み、
      **`next = currentStatus === 'unread' ? 'read' : 'unread'`** を算出する（`unread` → `read` /
      `read` → `unread` / `archived` → `unread` の 3 ケースを 1 式で満たす。Req 2.3 / 2.4 / 2.6）。
      `fetch('/v1/items/' + id + '/status', {method:'PATCH', headers: {...headers,
      'Content-Type':'application/json'}, body: JSON.stringify({status: next})})` を呼ぶ
    - 成功時:
      - card の `data-status` 属性を `next` に更新
      - **同一カード内の `button.mark-read-toggle` と `button.archive-toggle` の双方の
        `data-current-status` 属性も `next` に更新する**（次回 click 時の判定元 / Reviewer
        指摘 #1 反映。`data-status` のみ更新して `data-current-status` を残置すると、
        例えば unread→read 後に再度 mark-read-toggle を押した際、stale な `unread` を読んで
        誤って再度 `read` を送り、Req 2.4（read → unread）が機能しなくなる）
      - ボタンの label / aria-label を新状態に合わせて書き換え（mark-read-toggle: "Mark read"
        ⇄ "Mark unread"、archive-toggle: "Archive" ⇄ "Unarchive"）
      - `item-status-badge` のテキストと `data-status` 属性を更新
      - 現在の status タブ条件で非表示にすべき item は `<article>` 要素を fade-out で DOM
        削除（Req 2.8）
    - 失敗時: `toast.error('状態の更新に失敗しました')` + ボタンと card の元状態維持
      （`data-status` / `data-current-status` / label / aria-label のいずれも書き換えない / Req 2.7）
    - `button.archive-toggle`: 同様、`next = currentStatus === 'archived' ? 'unread' : 'archived'`。
      成功時は同じく card と 2 ボタンの `data-current-status` / label / aria-label / badge を更新
  - **NFR 1.3 視覚フィードバック 500ms（同期 visual ack）**:
    - click 直後（PATCH 応答前）に **同期的に** ボタン `disabled` 化と card に
      `is-status-updating` CSS class を付与する（応答完了を待たずに発火）。これにより
      ユーザー click から **同一 task tick の DOM 反映**として視覚フィードバックが返り、
      NFR 1.3 の 500ms 閾値は応答時間でなく **synchronous DOM 反映の遅延**として満たされる
    - 応答成功時: `is-status-updating` を外し、`data-status` / `data-current-status` / label /
      aria-label / badge を確定更新
    - 応答失敗時: `is-status-updating` を外し、ボタン `disabled` を解除、`toast.error` を表示
      （元状態を維持。Req 2.7 と NFR 1.3 を同時に満たす）
    - CSS（タスク 10 でカバー）: `.item-card.is-status-updating { opacity: 0.65; pointer-events: none; }`
      程度の即時表現で十分（design.md NFR 1.3 Traceability 参照）
  - 既存 `app.js` の keyboard shortcut handler は変更しない（設計確認事項 (c) により本 Issue
    では新規 shortcut なし）
  - **テスト追加（同 task 内）**: `static/items_status.test.mjs` を新規追加し、`node --test`
    で以下を検証（既存 `items_active_filters.test.mjs` のパターンを参考にする）:
    - `mark-read-toggle` を `data-current-status="unread"` カードで click → fetch が
      `/v1/items/<id>/status` PATCH を呼び、body が `{"status":"read"}`（Req 2.3）
    - `mark-read-toggle` を `data-current-status="read"` カードで click → body が
      `{"status":"unread"}`（Req 2.4）
    - **`mark-read-toggle` を `data-current-status="archived"` カードで click → body が
      `{"status":"unread"}`** を直接 assert（Req 2.6 のアーカイブ解除を回帰検証）
    - 成功時に **`data-status` も同一カード内の 2 ボタンの `data-current-status` も両方が**
      新状態に更新される（Req 2.8 + Reviewer 指摘 #1: 次回 click の判定元の同期回帰固定）
    - **連続 click 回帰**: `data-current-status="unread"` カードで `mark-read-toggle` を
      1 回 click → 成功応答 → 直後に **同じカードの同じボタンをもう一度 click** したとき、
      2 回目の PATCH body が `{"status":"unread"}`（read からの戻し）となることを assert する
      （stale な `unread` を読んで誤って 2 回連続で `{"status":"read"}` を送らないことを
      Req 2.4 / 2.6 の連続操作レベルで回帰固定 / Reviewer 指摘 #1 高）
    - 失敗時に元の `data-status` と元の `data-current-status` が両方維持される（Req 2.7）
    - `archive-toggle` を `data-current-status="unread"` カードで click → body が
      `{"status":"archived"}`（Req 2.5）
    - **`archive-toggle` を `data-current-status="read"` カードで click → body が
      `{"status":"archived"}`** を直接 assert（Req 2.5 を read 状態のカードに対しても
      適用することを回帰固定 / Reviewer 指摘）
    - `archive-toggle` を `data-current-status="archived"` カードで click → body が
      `{"status":"unread"}`（Req 2.6 のもう一方の経路）
    - **`mark-read-toggle` / `archive-toggle` のいずれの成功時でも、現在の status タブ条件
      （`[data-items-region].dataset.currentStatus` 等の test fixture で擬似的に "unread" /
      "archived" を設定）に一致しなくなった item の `<article>` 要素が DOM から fade-out で
      削除される**ことを assert（例: Unread タブで `mark-read-toggle: unread→read` 後に
      当該 card が DOM から消える / Archived タブで `archive-toggle: archived→unread` 後に
      当該 card が DOM から消える）。タブ条件に一致したままの場合は DOM 上に残ることも
      別ケースで assert（Req 2.8 のフィルタ条件に従った表示／非表示を回帰検証 / Reviewer 指摘）
    - **NFR 1.3 同期 visual ack 回帰**: `mark-read-toggle` を click した直後（fetch が pending
      `Promise` を返した state のまま、その後の `then` / `await` を進めず synchronous DOM 反映
      のみを観測する）、当該ボタンが `disabled` 化され、当該 card に `is-status-updating`
      class が付与されることを assert する。応答 resolve 後に `is-status-updating` が外れ、
      応答 reject 後にも `is-status-updating` が外れて元状態が維持されることを別ケースで
      assert（NFR 1.3 の 500ms フィードバックが「応答時間でなく synchronous DOM 反映の遅延」
      で満たされることを回帰検証 / Reviewer r6 指摘 #2 反映）
  - _Requirements: 2.3, 2.4, 2.5, 2.6, 2.7, 2.8, NFR 1.3_
  - _Boundary: Static_
  - _Depends: 4, 7_

- [ ] 9. static JS: items_status.js（タブ切替 + fragment 取得 + popstate + タブ active 同期）
  - `static/items_status.js` を新規作成。`static/items_active_filters.js` の pattern に揃える:
    - `[data-items-region]` 上の `__itemsFragmentInflight` slot を共有して AbortController を持つ
    - `<nav.status-tabs a[role="tab"]>` の click を delegated 捕捉 → `?status=` を書き換えた
      相対 URL を計算 → `history.pushState` → `X-Requested-With: ItemsFragment` で fragment 取得
    - **タブ active 状態の手動同期**: status-tabs は `items.html` 側にあり、fragment 取得で
      置換される `items_list.html` には含まれない。fragment 取得直後（および click 直後）に
      JS が `nav.status-tabs a[role="tab"]` を走査し、新 `?status=` 値に一致するタブに
      `aria-selected="true"` / `class="is-active"` を付与し、他のタブからは外す処理を行う
      （Req 3.7 の常時可視化、Req 3.8 のページ遷移後保持と矛盾しない描画維持）
    - popstate で `?status=` を読み取って fragment 取得（Req 3.8 の URL クエリ永続を戻る/進むに追従）。
      popstate 時も上記タブ active 状態の手動同期を行う
    - 修飾キー付き click（Cmd/Ctrl/Shift/Alt）は intercept せず既定動作を維持
  - `templates/items.html` の script 読み込み行に `<script src="/static/items_status.js?v={{assetVersion}}" defer></script>` を追加
  - **テスト追加（同 task 内）**: `static/items_status_tabs.test.mjs` を新規追加し、`node --test`
    で以下を検証:
    - タブ click で URL が `?status=...` に切り替わる
    - fragment fetch が `X-Requested-With: ItemsFragment` を含む
    - **タブ click 直後（および fragment fetch 完了後）に、新 `?status=` 値に一致するタブだけが
      `aria-selected="true"` / `is-active` を持つ**ことを assert（Req 3.7 / 3.8 の active 同期回帰）
    - **`?status=` を **未指定**にして popstate / フルページロードを擬似発火させたとき、`Unread`
      タブが `aria-selected="true"` / `is-active` を持ち、他のタブからは外れることを assert**
      （Req 3.1 と design の「`?status=` 未指定時は既定 unread」を popstate 経路で落とさない
      ことを回帰固定 / Reviewer 指摘）
    - popstate で fragment 再取得が起き、タブ active 状態も新 URL に追従して更新される
    - 連続切替時に AbortController で前段が abort される（race 防止）
    - **既存クエリ併用の保持**: 初期 URL が `?q=foo&tag=bar&sort=created_at&per_page=30&page=2`
      の状態でタブを Archived に切り替えたとき、遷移先 URL が `?q=foo&tag=bar&sort=created_at&per_page=30&page=2&status=archived`
      となり、`q` / `tag` / `sort` / `per_page` / `page` の各既存クエリが落ちずに **保持** される
      ことを assert する（Req 3.6 の併用を popstate / pushState 双方で回帰固定 / Reviewer 指摘）。
      逆方向（Archived → Unread の切替時）でも同様に既存クエリが保持されることを別ケースで assert
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
      を併記する（Req 4.4 の色のみ依存禁止）
    - `.item-card.failed` と `.item-card[data-status="archived"]` が同時に成立しても破綻
      しないことを確認（failed の border-left + archived の背景弱化が共存）
    - **`.item-card.is-status-updating`** を新規追加（`opacity: 0.65; pointer-events: none;`
      程度の即時表現）。これはタスク 8 が click 直後に synchronous で付与する class で、
      NFR 1.3 の 500ms 視覚フィードバックを「応答時間でなく synchronous DOM 反映」で満たすため
      （design.md NFR 1.3 Traceability / Reviewer r6 指摘 #2 反映）
  - light / dark テーマ両方で視覚区別が成立することを目視確認
  - **テスト追加（同 task 内）**: CSS のみのタスクのため、視覚回帰テストは本リポジトリの既存
    pattern上手動目視で確認する（既存 #12 / #115 / #117 と同じ運用）。Go test での追加は不要
  - _Requirements: 4.1, 4.2, 4.3, 4.4, NFR 1.3_
  - _Boundary: Static_
  - _Depends: 6, 7_

## Verify

本 spec の実装後、watcher（stage-a-verify gate）が再実行すべき verify コマンドを以下の
構造化ブロックで宣言する。Go test と golangci-lint と Node.js 拡張テストの 3 系統を順次実行する。
新規追加する `static/items_status.test.mjs`（タスク 8）と `static/items_status_tabs.test.mjs`
（タスク 9）を node --test 引数に含め、本機能 JS テストがゲートで実行されるようにする。

<!-- stage-a-verify -->
```sh
go test ./... && golangci-lint run && node --test static/items_active_filters.test.mjs static/items_search.test.mjs static/items_tags.test.mjs static/items_fragment_race.test.mjs static/items_status.test.mjs static/items_status_tabs.test.mjs
```

### Integration test の取扱（stage-a-verify gate スコープ外）

`internal/store/store_item_status_test.go`（タスク 3）は `//go:build integration` tag 付きで
記述するため、上記 `go test ./...` では **実行されない**（既存 `items_active_filters_integration_test.go`
と同様の運用）。実 PostgreSQL を要するため、本ブロックには含めない（watcher 環境では DB を
spin-up しない方針 / `.kiro/steering/structure.md` 準拠）。

これらは以下のいずれかで担保する:

- 開発者ローカル: `go test -tags=integration ./internal/store/... ./internal/server/...`
  （`docker compose up -d postgres` で DB を起動した状態で実行）。`internal/server/items_status_test.go`
  の integration tag 付きハンドラテスト（タスク 4 の **4 件**: `TestHandleSetItemStatusSuccessReturns200` /
  `TestHandleSetItemStatusLogsTransitionFields` / `TestHandleSetItemStatusNotFoundForMissingID` /
  `TestHandleSetItemStatusOtherUserItemReturns404`）も同コマンドの対象に含まれる（r6 で
  200 / log の 2 件を integration tag 側へ追加 / Reviewer r6 指摘 #3 反映。`*store.Store` が
  差し替え不能な現行構造下で `UpdateItemStatus` の `prev` 返却を実 DB に依存させているため）
- Reviewer フェーズ: 必要に応じて Reviewer が同コマンドを手元で実行し AC カバーを確認する
- 既存 CI（`.github/workflows/ci.yml`）には integration tag 対応が無いため本 PR では追加しない
  （integration job 化は別 Issue で扱う方針、Out of Scope）

### Performance verification (NFR 1.1 / 1.2) — 手動検証

design.md「Performance / Performance & Scalability」節および NFR 1.1 / 1.2 の閾値（10,000 件
/ user 規模で Unread 初期表示が本機能導入前比 +20% 以内、タブ切替 1 秒以内）を、index 追加のみで
満たせていることを開発者ローカルで確認する（Reviewer 指摘 #9 / design.md:753-755 を
tasks 側に落とし込む）。本検証は単独 task として切り出すと Budget overflow check の 10 件閾値を
超えるため、Verify 節の **deferred manual step** として扱う。

検証手順:

- `psql` で開発 DB に対し以下を実行し、`items_user_status_idx` が選択されていることを確認:
  ```sql
  EXPLAIN (ANALYZE, BUFFERS)
  SELECT id, url, title, status FROM items
  WHERE user_id = $1 AND status = ANY('{unread}')
  ORDER BY created_at DESC LIMIT 30;
  ```
  Index Scan / Index Only Scan のいずれかが選択され、Seq Scan が出ないこと
- 10,000 件 / user 規模の seed データを生成し、ライブラリ初期表示
  （`/ui/items?status=unread`）の応答時間 p95 を本機能導入 **前** ブランチと **後**
  ブランチで比較し、+20% 以内（NFR 1.1）に収まることを記録
- タブ切替（`?status=archived` への fragment 取得）の応答時間が 1 秒以内（NFR 1.2、
  10,000 件 / user 既定ページサイズ前提）に収まることを記録

記録の置き場: `docs/specs/119-read-archived/impl-notes.md`（Developer フェーズで新規作成）
の「Performance verification (NFR 1.1 / 1.2)」見出し配下に、計測環境（PostgreSQL バージョン、
シード件数、ハードウェア概要）と計測値（前後比較）を貼り付ける。stage-a-verify gate の
自動コマンドには含めない（10,000 件 seed と前後ブランチ比較は CI 上で現実的に再現困難な
ため）。Reviewer は impl-notes.md の記録を確認することで AC カバーを判定する。
