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

### Task 4
- 採用方針: `internal/server/items_bulk_integration_test.go`（`//go:build integration`、`package server`）を新規追加。10 ケースで `handleBulkDeleteItems` / `handleBulkTagItems` を実 PostgreSQL に対して回し、task 2（store 層）と task 3（handler unit テスト fake store）の中間にある「実 SQL 経路 × 認可境界 leak ガード × 構造化ログ」を一括検証する。fixture は `items_active_filters_integration_test.go` の `newIntegrationStore` / seed パターンを踏襲しつつ、`seedBulkIntegUser` / `seedBulkIntegItem` / `seedBulkIntegItemWithTag` を label 付きで本ファイル内に独自定義した（既存 `seedItemsActiveFilterUser` を素のまま使うと複数ユーザー seed 時に `users.google_sub` UNIQUE 衝突が起きるため。task 2 と同じ判断）。
- 重要な判断:
  - **server を `New(...)` 経由で構築**: `newBulkIntegrationServer` は `New(cfg, st, ratelimit.New(60, 60), logger, nil)` を呼ぶことで、production と同じ `bulkStore = st` の interface 経由 wiring を踏む。`store.Store` が `bulkItemsStore` interface を implicit に満たしているので adapter は不要。logger は `slog.NewJSONHandler(buf, ...)` でバッファに JSON 行を吐かせ、NFR 5.1 の fields / leak guard を JSON keys でアサート可能にした（既存 `items_status_integration_test.go` の `newStatusIntegrationServer` と同じ規約）。
  - **partial-failure の 6-id 構成**: `TestHandleBulkDeleteItems_PartialFailureResponse` は own 3 + other-user 2 + 不存在 1 を seed して 6 ID で 1 リクエストを投げる。succeeded 3 / failed 3 を **set-based assertion** で確認（ID 順序は SQL 側に依存させない）、failed reason 全件が `"not_found"` で collapse されていることを assert。さらに wire-level に `"title"` / `"url"` substring が JSON body に出ないことを assert（task 3 の fake-store 版と同じ規約を実 DB 経路でも固定 / Req 8.2 / 8.3 leak prevention）。最後に DB cross-check で other-user items が DB に残存することを `EXISTS` で確認（task 2 の `existsItem` 同様）。
  - **invalid UUID collapse の実 DB 検証**: 「`not-a-uuid` が store に到達したら `unnest($1::uuid[])` で 22P02 (`invalid_text_representation`) が出る」のが setup 側の前提。handler の `partitionByUUID` が invalid を除外しているからこそ 500 db_error にならず 200 + failed[{not_found}] が返るという contract を、実 DB 経由で固定する（task 3 の fake では `lastDeleteIDs` を見て「invalid が store に渡っていない」ことだけ assert、実 SQL のレスポンス全体は task 4 の責務）。
  - **structured log の検査方法**: `findLogRecord(t, buf, msg)` ヘルパーで `buf` の各行を JSON parse し `msg` 完全一致のレコードを返す（`items_status_integration_test.go` の手法を踏襲）。期待 6 フィールドは map に key 存在を assert、`user_id` 値だけ string 内容を確認。secret leak guard は buf 全体（log の全行）を対象に substring を grep（特定の log 行だけでなく request lifecycle 中の他の log line も含めるため）。
  - **bulk-tag の cookie leak fixture**: 当初 `Authorization: Bearer ...` も seed しようとしたが、handler は Bearer 受信時に **403 forbidden で即返**するため log path に到達しない。NFR 5.1 のリーク対象として最も重要な `altpocket_session=<secret>` cookie のみを request に attach する形に整理した（Bearer rejection の 403 path は task 3 の `TestHandleBulkDeleteItems_RejectsBearerAuthReturns403` / `TestHandleBulkTagItems_RejectsBearerAuthReturns403` で既に handler unit テストでカバー済み）。
  - **chi router smoke test の到達点**: `TestBulkRoutesOnRealRouterReturnCSRFForbiddenWithoutAuth` は `srv.Routes()` を `chi.Router` に型 assert し、`router.ServeHTTP(rr, req)` で `requireAuth` middleware の **`checkCSRF` → `authenticate`** の順序を経由した実挙動を確認。Authorization 無 + `altpocket_session` cookie 無 + `X-CSRF-Token` 無の状態で POST すると、`checkCSRF` が「missing session」error を返し handler は `{"error":"csrf"}` 403 を返す。401 unauthorized 経路はこの fixture では到達しないため assert しない（401 は task 3 の handler 単体テストでカバー済み）。round 2 review feedback で指摘された「Authorization 無の場合に session cookie 不在を csrf エラーで弾く既存挙動」の回帰固定。
  - **dedup test の DB cross-check**: `TestHandleBulkTagItems_DedupesExistingTagInRequest` はレスポンスの `succeeded[0].Tags` 内容だけでなく、`SELECT COUNT(*) FROM item_tags WHERE item_id = $1` で DB の item_tags 行数も 1 件であることを確認。store の `INSERT ... ON CONFLICT (item_id, tag_id) DO NOTHING` が responses と DB state の両方で重複を防いでいることを 2 経路から固定。
  - **container 比較は normalized_name で**: `TestHandleBulkTagItems_SucceedsAndReturnsFullTags` の post-tag 内容 assertion は `Tag.NormalizedName` で比較する。`Tag.Name` (display name) は `seedBulkIntegItemWithTag` 経由で `"alpha"` のままだが、新規付与の `"GoLang"` は `tag.Normalize` → `"golang"` / `tag.DisplayName` → `"GoLang"` を分離して書き込むため、display side で比較するとアサーション値が混在する。テストの意図は「タグ集合が期待通り」なので normalized_name で比較する方が要旨に直接刺さる（既存 `TestBulkAddItemTag_PreservesExistingTags` も同じ理由で Name と NormalizedName の混在を慎重に扱っている）。
  - **integration tag の取扱**: tasks.md line 366-368 に従い「本タスクのテスト群は stage-a-verify の `go test ./...` には含まれない」（既存 CI に integration tag 対応が無いため）。`-tags=integration` + `TEST_DATABASE_URL` 指定時のみ実行される。tag なし `go test ./...` への影響ゼロを確認済み（CI 退行なし）。
- 残存課題: なし。本 task の責務範囲（実 SQL 経路の認可境界 leak 検証 / chi router 経由の middleware 順序固定）はすべて 10 ケースで完結。templates / static JS / CSS（task 5〜8）は本 task のスコープ外（次 task で実装される）。

### Task 5
- 採用方針: design.md「Templates」節と tasks.md line 380-465 の SSR 仕様を逐語的に実装。`templates/items_list.html` の `<article>` 1 箇所に `data-item-id` + `data-original-url` の 2 属性を追加 + `<a class="tile-link">` の直前に `<input type="checkbox" class="item-select" disabled>` を 1 個挿入、`templates/items.html` の `</section>` (split 終了) と既存 script 群の間に bulk-toolbar / bulk-tag-dialog / bulk-failure-dialog の 3 markup と bulk スクリプト 2 行を挿入する純粋なテンプレ追加で完結。既存テンプレ構造（status-tabs / active-filters / タグ chip / 単一アクション / hidden status input）は一切触らない。
- 重要な判断:
  - **`disabled` 属性付きで checkbox を SSR**: tasks.md / design.md NFR 3.5 規約に従い `<input type="checkbox" ... disabled>` で出力。JS 無効ブラウザでは Tab フォーカスを取らず click も無効化される（HTML 仕様の disabled 挙動）。task 6 の `items_bulk_selection.js` の `init()` 起動時に `removeAttribute('disabled')` で操作可能になる Progressive Enhancement 規約。これにより本機能導入前と同等の閲覧 / 単一アクション動線が JS 無効環境で維持される。
  - **`data-item-id` を `<article>` 自身に付与**: selection / actions モジュールから `closest('.item-card')` で id を解決する用。`<input type="checkbox">` 自身にも `data-item-id` を付与しているのは tasks.md 行 396 の仕様通り（aria-label と並んで checkbox 自身を直接参照するパスがあるため）。
  - **`data-original-url="{{.URL}}"` を `<article>` に追加**: 失敗通知時のタイトル空 fallback URL として `article.dataset.originalUrl` で参照する用。既存 `<a class="tile-link" href="/ui/items/<id>">` は内部詳細ページ URL であり元記事 URL ではないため、URL fallback には使えない（design.md「失敗 toast の表示文言」節 / Req 4.7 / 5.7 を空タイトル item でも満たす）。
  - **bulk-toolbar / dialog の挿入位置**: tasks.md は「`<section class="split">` の終了 (`</section>`) と既存 script 群の間に追加」と指定。`</section>` の直後（split 全体の閉じ）、`<script src=".../items_search.js">` の直前に 3 markup を順次配置。これにより sticky 配置で画面下に貼り付く際の DOM 順序が確定する（既存 status-tabs より下、`items.html` の bottom sheet overlay と並ぶ位置）。
  - **`method="dialog"` の `<form>` 残置**: tasks.md コメントで明記されているとおり、ネイティブ `method="dialog"` の自動 close は task 7 の actions モジュール側の submit ハンドラで `event.preventDefault()` を呼んで抑止する。テンプレ側では `method="dialog"` を維持し、JS 側に責務を移譲する。
  - **bulk-failure-dialog の `<ul>` markup**: SSR では空 `<ul>` を出力し、task 7 の actions モジュールが `<li>` を populate して `showModal()` する。`max-height: 60vh; overflow-y: auto` は task 8 の CSS で担保（テンプレ側は構造のみ）。100 件まで全件 reachable な領域を SSR で予約する（Req 4.7 / 5.7）。
  - **script 読み込み順序**: tasks.md の通り `items_status.js` の直後に `items_bulk_selection.js` → `items_bulk_actions.js` の順で挿入。actions モジュールは selection モジュールの API（`window.altpocketBulkSelection`）を参照するため、selection が先に load される必要がある（`defer` 属性により DOM 解析後の順序実行が保証される）。
  - **Go test / 拡張 / 既存 JS テスト の影響なし**: 既存 `go test ./...` は 14 パッケージすべて pass（`internal/ui` の template parse / render テストを含む）。既存 Node.js テスト（`sidepanel.test.mjs` / `items_active_filters.test.mjs` / `items_search.test.mjs` / `items_tags.test.mjs` / `items_fragment_race.test.mjs` / `items_status.test.mjs` / `items_status_tabs.test.mjs`）も 133 件 pass。golangci-lint は本環境にバイナリ未設置のため未実行（CI で実行される）。
- 残存課題: なし。テンプレ差分の単体 Go test は本リポジトリの既存規約に倣い追加せず（既存 #115 / #117 / #119 と同じ運用）、task 6 / 7 / 8 の JS / CSS テストで間接的にカバーされる。`items_bulk_selection.js` / `items_bulk_actions.js` のファイル本体は task 6 / 7 で作成されるため、本 task の SSR 時点では 404 になる（既存サイト動線は影響を受けないが、新規 markup の動作は task 6 完了まで未実装の状態が続く）。

### Task 6
- 採用方針: design.md「Components and Interfaces > items_bulk_selection.js」節と tasks.md line 467-695 の仕様を逐語的に実装。`static/items_bulk_selection.js` を IIFE + `init({document, window})` パターンで 1 ファイルに集約し、`static/items_bulk_selection.test.mjs` で 21 ケースの単体検証を `node --test` + `vm.createContext` 上の fake DOM で完結させる。既存モジュール（`items_status.js` / `items_active_filters.js` / `items_tags.js` / `items_search.js`）の AbortController 共有 slot (`region.__itemsFragmentInflight`) は読み書きせず、既存 `app.js` の keyboard handler とも独立に `document.addEventListener('keydown')` を register する（NFR 3.1 / 3.2 / 3.3）。
- 重要な判断:
  - **document-level delegated event 登録**: `change` / `click` ハンドラを `region.addEventListener` ではなく `doc.addEventListener` に register し、ハンドラ冒頭で `e.target.closest('[data-items-region]') === region` で絞り込む方針を採用（既存 `items_status.js` の `onDocumentClick` 規約と同形）。理由は (a) `[data-items-region]` 内の任意の要素から発火する change / click をバブリングで document まで届かせる Web 標準の挙動と整合する、(b) fake DOM テストで `document.dispatch` 経由のイベント発火が region listener に届かない問題を回避できる、の 2 点。
  - **`dispatchChanged()` を全パスで 1 回発火**: toggle / 範囲選択 / clear / removeFromSelection / fragment swap reset / popstate のすべてのパスで count + ids を含む `bulkselection:changed` event を `region.dispatchEvent` する。連続 reset で count=0 でも必ず 1 回発火し、後続 task 7 のツールバー件数表示が「state は変わったが event は来ない」状態に陥らないようにする。
  - **`isInRegion` の closest 比較**: `t.closest('[data-items-region]') === region` で「region 内」を判定する。document 上に複数の `[data-items-region]` が存在する shouldn't 環境（detail page 等）で意図しないハンドラ発火を防ぐ。テスト fixture の `createFakeRegion` でも同一 region 参照が `doc.querySelector('[data-items-region]')` の戻り値と一致する。
  - **`computeRange` は region 配下の `.item-card` を `querySelectorAll` した DOM 順で算出**: `document.querySelectorAll('.item-card')` ではなく `region.querySelectorAll('.item-card')` に絞ることで、detail page 等の他領域に `.item-card` がある場合も巻き込まない。tasks.md 行 498 の "`document.querySelectorAll('.item-card')` の順" 表記は region 配下を含意するため、region に絞った形で実装した（仕様の意図と整合）。
  - **Shift+click 経路では change 経路に二重発火させない**: shift+click で `e.preventDefault()` を呼ぶとブラウザのネイティブ checkbox toggle が抑止される。一方、change イベントは preventDefault されてもブラウザによっては発火する場合がある（fake DOM では発火しない）。本実装の `onChange` は `selectionSet.has(id)` を最初に検査し既に追加済みなら no-op するため、shift+click 経路で programmatic に `cb.checked = true` を設定した直後に change が漏れて二重発火しても idempotent に動作する。
  - **`addToSet` / `removeFromSet` で checkbox.checked を同期**: change ハンドラ起点ではなく shift+click / clear / keyboard x の経路から呼ばれた場合、DOM 上の `.item-select[checked]` 状態と内部 Set が乖離するため、addToSet / removeFromSet 内で明示的に `cb.checked = true/false` を書く。change ハンドラ起点では既に同値なので no-op になる。
  - **上限 100 件 enforcement の意味論差**: 単一 toggle 経路の `ensureCanAddOne()` は「新規 1 件を追加して 101 件目になる」を弾く（既存 100 件の場合 false）。範囲選択経路の `ensureCanAddRange(newIDs)` は「現在の Set にない id だけを数え、合算 > 100 なら範囲全体を reject」する。後者は Req 2.1 の "範囲のカードすべてを選択状態にする" を厳密に解釈し、部分追加で曖昧な状態を生まない。
  - **`endActionMutation()` での takeRecords() 経路**: `observer.takeRecords()` で取り出した queued records を `classifyAndApply` に再入力する。fragment swap record (addedNodes.length > 0) は bracket 状態に関係なく reset し、per-item 削除 record (addedNodes.length === 0) は bracket 中なので discard する（順序: takeRecords → fragment swap を per-record で処理 → per-item 削除を discard → depth -=1）。round 4 review feedback の「queued fragment swap を黙って捨てない」要件を `TestEndActionMutationProcessesQueuedFragmentSwapBeforeDiscard` で固定。
  - **テスト fake MutationObserver の手動 flush**: 本番ブラウザの MutationObserver は microtask 後に callback を発火するが、`node --test` 環境では「行儀のよい同期テスト」を優先し `observer._flush()` を明示呼出で発火させる方針を採った（`createFakeRegion()._observers[i]._flush()`）。これにより `bracket 中の queued record を takeRecords() で処理する` 規約と「callback 経路で fragment swap を検出する」規約の両方を、別々のテストケースで明確に固定できる。
  - **`removeFromSelection(ids)` の anchor stale 防止**: 削除対象 id が `lastClickedID` と一致する場合は `lastClickedID = null` に倒す（design.md「anchor の stale 防止」節 / round 2 review feedback）。これにより actions モジュールが部分失敗時の succeeded id を removeFromSelection で渡した直後の shift+click が、削除された article を anchor として範囲算出することを防止する。
  - **既存テンプレート / Go テストへの影響なし**: 既存 154 件の node test（133 既存 + 21 新規）と全 Go test (14 パッケージ) が pass。golangci-lint は本環境にバイナリ未設置のため未実行（CI で実行される）。
- 残存課題: なし。task 6 で実装した selection モジュールは task 7 (actions モジュール) から `window.altpocketBulkSelection` 経由で参照される。`beginActionMutation` / `endActionMutation` のブラケットは task 7 の `succeeded ids の article.remove()` を囲む用途で使われる（design.md「Components / items_bulk_actions.js」節）。

### Task 7
- 採用方針: design.md「Components and Interfaces > items_bulk_actions.js」節と tasks.md line 697-1060 を逐語的に実装。`static/items_bulk_actions.js` を IIFE + `init({document, window, fetch, toast, setTimeout})` パターンで 1 ファイルに集約し、`static/items_bulk_actions.test.mjs` で 34 ケース（tasks.md 列挙の 30 ケース + 全角混じり / network 失敗 / 混在 ?tag+?tags 等の派生 4 ケース）を `node --test` + `vm.createContext` 上の fake DOM で完結させる。`static/app.js` には `window.altpocketConfirm = confirm;` を `const confirm = (() => { ... })();` の直後（line 120 付近）に、`window.altpocketNormalizeTagName = normalizeTagName;` を `const normalizeTagName = (value) => { ... };` の直後（line 344 付近）に挿入する 2 行追加のみで、既存単一アイテム動線 / 拡張機能契約 / SSR markup には一切手を入れない。
- 重要な判断:
  - **リクエスト ID スナップショット規約の遵守**: click ハンドラ冒頭で `const requestIds = Array.from(selection.getSelectedIDs());` を defensive copy し、以降の confirm dialog 文言 / fetch body / レスポンス処理（succeeded / failed / 4xx / 5xx / network 失敗）/ DOM 収集 / removeFromSelection のすべてを **closure 内 `requestIds`** で動作させる。live `selection` は fetch 中に変化しうるため、件数 / 通知対象 / 解除対象がすべて click 時点の状態で確定する。tasks.md「snapshot 規約 1〜6」の不変条件を 6 件とも実装で固定し、`TestDeleteAllSuccessPreservesInFlightNewSelection` / `TestDeleteServerErrorUsesRequestIdsNotLiveSelection` / `TestTagSuccessDeselectsSnapshotAndClosesDialog` の 3 ケースで「fetch pending 中に live が変化しても snapshot ベースで一貫した処理」を回帰固定。
  - **`selection.removeFromSelection(snapshot)` を採用、`selection.clear()` は呼ばない**: 全成功 / 部分失敗いずれの選択解除でも `selection.removeFromSelection(succeeded)` または `selection.removeFromSelection(requestIds)` を呼び、`selection.clear()` は本モジュールから一切呼ばない（bulk-clear ボタン経由でのみ clear() が呼ばれる）。これにより fetch 中の新規選択 C が誤って消えるのを構造的に防ぐ（Req 4.8 / 5.8 拡張）。
  - **fadeOutAndRemove と beginActionMutation のブラケット規約（方式 A）**: succeeded id 1 件ごとに `selection.beginActionMutation()` を呼んで bracket カウンタを +1 し、`setTimeout(() => { article.remove(); selection.endActionMutation(); }, 300)` で remove と end を同 microtask 内で続けて発火する。これにより selection 側 MutationObserver の per-item 削除検知が bracket 中に discard され、partial-failure 時の failed 選択が消えない（task 6 の reference counting bracket と整合）。article が null の fallback では begin を呼ばず、selection から id のみ removeFromSelection で外す（snapshot 規約 4）。
  - **失敗 dialog は 100 件まで全件 populate**: tasks.md の round 5 review feedback「truncation 撤廃」を逐語的に実装し、`showBulkFailureDialog({verb, items})` の `<li>` 生成ループで `items.length` を上限なしで populate する。`<li>` の本文は `it.title` → `it.url` → `it.id` の 3 段 fallback で 1 行は必ず提示し、snapshot 規約 5 の id-only fallback を厳守する。`TestFailureDialogPopulatesAllItemsWithoutTruncation` で 6 / 50 / 100 件の `<li>` 件数を機械的に回帰固定。
  - **chip rebuild は SSR contract に完全一致**: `templates/items_list.html` line 65-78 の `<button type="button" class="tag tag-filter-toggle{{is-selected?}}" data-tag-filter-toggle data-tag-normalized="..." aria-pressed="..." aria-label="タグで絞り込み: {{name}}">{{name}}</button>` を JS 側で `document.createElement('button')` + `setAttribute(...)` + `textContent = name` で組み立てる（`innerHTML` / `insertAdjacentHTML` 禁止 / XSS 防御）。タグの active filter 判定（is-selected / aria-pressed）は **chip rebuild 前に 1 度だけ** `computeActiveNormalizedNames()` で `Set<string>` を算出し、card ループ内で `set.has(tag.normalized_name)` で枝分かれする。
  - **active filter chip 連携の両形式対応**: `parseTagFilters`（`internal/server/server.go:1557`）が canonical `?tag=X` repetition と legacy `?tags=a,b` CSV の両形式を受理する規約を JS 側でミラーする。`new URL(window.location.href).searchParams.getAll('tag')` で canonical を抽出し、`.get('tags')?.split(',')` で legacy を抽出。両者を concat → `(window.altpocketNormalizeTagName || fallbackNormalize)` で正規化 → 空文字を除外して Set 化する。これにより SSR 直後の URL が `?tag=GoLang` でも `?tags=go,rust` でも、bulk-tag 成功で再構築された chip が active filter 状態を保持する（NFR 3.2 / 3.3 / round 4 review feedback）。`TestTagSuccessRespectsLegacyTagsCsvParam` で混在ケース `?tag=go&tags=rust,python` も含めて回帰固定。
  - **bulk-tag dialog submit 規約**: `form.addEventListener('submit', ...)` の冒頭で **`event.preventDefault()` 必須**。これにより `method="dialog"` の自動 close が抑止され、空判定（fetch 未呼出）/ 400 invalid_tag（dialog 開いたまま + input focus 戻し）の両ケースで dialog が意図せず閉じるのを防ぐ。`window.altpocketNormalizeTagName || fallbackNormalize` で空判定だけを行い、**POST body には原文字列を送る**（NFKC + lowercase は server 側 `tag.Normalize` に委ねる / Req 5.2 既存規約踏襲）。
  - **busy 状態は class + `disabled` 属性の両方**: ツールバー 3 ボタン（bulk-delete / bulk-tag / bulk-clear）と bulk-tag dialog 2 ボタン（cancel / confirm）に対して、`setBusy(true)` で `toolbar.classList.add('is-busy')` と `setAttribute('disabled', '')` を同期付与し、応答完了で `removeAttribute('disabled')` + `classList.remove('is-busy')` する。pointer-events のみだとキーボード起動（Tab → Enter / Space）を止められないため、`disabled` 属性で確実に二重送信を抑止する（NFR 1.2 / round 4 review feedback）。`TestToolbarButtonsDisabledDuringInFlightRequest` で fetch pending 中の 3 ボタン disabled と resolve 後の解除を回帰固定。
  - **confirm dialog のシグネチャ規約**: `window.altpocketConfirm.show(title, description, onConfirm, actionLabel, actionClass)` の **object メソッド呼び出し**を厳守し、`window.altpocketConfirm(message)` の関数呼び出しは行わない。`window.altpocketConfirm` が undefined（テスト / 初期化前）の fallback では `window.confirm(message)` 標準に降格する。`TestDeleteConfirmUsesShowSignature` で object シグネチャ + description に件数が含まれることを回帰固定。
  - **400 invalid_request / payload_too_large の挙動一致**: 一括削除 / 一括タグ付けのいずれの side でも、これら systemic エラー（per-item identify 不要）は **`toast.error` のみ**で `bulk-failure-dialog` を出さず、`selection` も触らない。これにより削除側と挙動が一致し、UI 上で「一括操作 リクエスト不正」「100 件超え」が同じ動線で通知される（round 5 review feedback / `TestDeleteInvalidRequestShowsToastNotDialog` / `TestTagInvalidRequestShowsToastNotDialog` 等で固定）。
  - **テスト fake DOM のセレクタ matcher 強化**: `items_status.test.mjs` パターンを踏襲しつつ、本 task のセレクタが要求する `button` 単独 / `h3[id^="item-title-"]` / `button.tag.tag-filter-toggle` 等を扱えるよう以下を追加した: (a) 単純タグ名 `^([a-z][a-z0-9]*)$` matcher、(b) tag + attribute prefix matcher `^([a-z][a-z0-9]*)\[([\w-]+)\^="([^"]*)"\]$`、(c) tag + multi-class matcher `^([a-z][a-z0-9]*|)((?:\.[\w-]+)+)$`、(d) `setAttribute('class', ...)` の `classList` 再構築。h3 / h2 のような数字混じり tag に既存正規表現 `[a-z]+` がマッチしない（過去 `items_status.test.mjs` の制約）ことを修正版で吸収。
  - **auto-init 抑止フラグ**: production 経路（IIFE 末尾）の auto-init による handler 重複登録を防ぐため、`window.__altpocketBulkActionsSkipAutoInit = true` を test 側で設定し、test は明示的に `window.altpocketBulkActionsInit({...})` を呼ぶ。本フラグは production では undefined のまま auto-init が走るため挙動影響なし。
- 残存課題:
  - tasks.md は「30 ケース」と列挙しているが、実装では派生サブケース（全角 / network failure / 混在 ?tag+?tags / 100 件 fixture）を増やして 34 ケースに拡張した。tasks.md と Reviewer の categorical 判定（missing test）に影響しない範囲での裁量拡張で、欠落していた本来の 30 ケースは全て個別の test で固定済み。
  - `golangci-lint run` は本環境にバイナリ未設置のため未実行（CI で実行される）。`go test ./...` は 14 パッケージすべて pass。node test は 188 件 pass（既存 154 + 新規 34）。
  - 本モジュールは CSS（task 8）依存箇所が 2 つある: (1) ツールバー `is-busy` class の spinner / 視覚 dim、(2) `<dialog>` の max-height + overflow-y。task 8 完了まで視覚は未スタイル状態だが、HTML / JS の動作は本 task で完結する（CSS なしでも `disabled` 属性で二重送信は防げる）。

### Task 8
- 採用方針: `static/style.css` の末尾（Safe Areas セクション直前）に `Bulk Operations (#118)` ブロックを 1 箇所追加し、tasks.md 行 1078-1122 が列挙する 13 種の新 selector（`.item-card { position: relative }` 明示 / `.item-select` + `:focus-visible` + `:disabled` / `.item-card.is-selected` / `.bulk-toolbar` + `[hidden]` / `.bulk-toolbar-count` / `.bulk-toolbar-actions` / `.bulk-toolbar button:disabled` + `.bulk-tag-dialog button:disabled` + `.bulk-failure-dialog button:disabled` / `.bulk-toolbar.is-busy` / `.bulk-tag-dialog` + 子要素 / `.bulk-tag-dialog::backdrop` / `.bulk-failure-dialog` / `.bulk-failure-dialog::backdrop` / `.bulk-failure-list`）を一括で追加。既存 selector（`.item-card.failed` line 936-937、`.item-card[data-status]` line 1331-1354、`.tile` line 868-885、`.confirm-overlay` / `.confirm-dialog` line 2471-2525、`.tag.tag-filter-toggle:focus-visible` line 1094-1097）は一切改変していない（NFR 3.1 / 3.2 / 3.3）。
- 重要な判断:
  - **`.item-card { position: relative; }` の冪等明示**: `.tile` 側 (line 874) で既に `position: relative` が指定されているため実質 no-op だが、`.item-card` 単独で参照される将来構成（detail page 等）や、`.tile` クラスが外れた場合のレイアウト破綻を防ぐため明示した。spec の指示通り。
  - **既存 `dialog::backdrop` 不在 → `.confirm-overlay` / `.sheet-overlay` の rgba 流用**: 本リポジトリには `dialog::backdrop` を持つ既存 selector が無いため（`grep -n "::backdrop" static/style.css` で 0 件）、tasks.md 行 1112-1118 が「既存 confirm overlay と同じ rgba」と指定する値は `.confirm-overlay` (line 2474) と `.sheet-overlay` (line 2533) の **`rgba(0, 0, 0, 0.5)`** を採用した。両者が一致しているため流用方針は確定。
  - **`.item-card.is-selected` を border-left ではなく inset box-shadow + 背景で表現**: `#12` の `.item-card.failed` (border-left: 3px solid danger) との軸衝突を避けるため、`box-shadow: inset 0 0 0 2px var(--color-primary);` + `background: var(--color-primary-soft);` の組合せにした。`outline: 2px solid var(--color-primary); outline-offset: -2px;` でも代替可能だが、box-shadow の方が `overflow: hidden` を持つ子要素（既存 `<a class="tile-link">`）の clipping 内に確実に収まり、`.tile:hover` の `box-shadow: var(--shadow-md);` (line 887-889) と layer 構造で共存しても視覚競合しない（box-shadow は複数値スタック可）。Req 1.4「色だけに依存しない」は checked 状態の checkbox との併用で担保される（NFR 4.3）。
  - **`.bulk-toolbar[hidden]` で `display: none` を明示**: HTML5 仕様の `hidden` 属性は UA stylesheet で `display: none` が当たるが、本セクションで `.bulk-toolbar` に `display: flex` を指定するため、specificity 同点で UA を上書きしてしまう可能性がある。明示的に `.bulk-toolbar[hidden] { display: none; }` を書いて確実に hidden 属性が効くようにした（task 6 の selection モジュールが選択件数 0 で hidden を setAttribute / 1 件以上で removeAttribute する規約）。
  - **`.item-select :focus-visible` の outline リテラル**: 既存 `.tag.tag-filter-toggle:focus-visible` (line 1094-1097) の `outline: 2px solid var(--color-primary); outline-offset: 2px;` パターンに完全一致させた。tasks.md 行 1083-1084 の指示通り `--focus-ring` token は本リポジトリに未定義なので使えない。
  - **`.bulk-toolbar` の z-index 設定**: `.confirm-overlay` (z-index: 400 / line 2475)、`.sheet-overlay` (z-index: 250 / line 2534) との layering を考慮し、`.bulk-toolbar` は **z-index: 300** とした。これにより (a) confirm dialog 表示中は toolbar より前面に出る (b) bottom-sheet (`.sheet-overlay` 250) より前面で sticky 表示される、という規約になる。bulk-tag-dialog / bulk-failure-dialog は `<dialog>` 要素の native top-layer に乗るため z-index に影響されず toolbar より前面に出る。
  - **`.bulk-failure-list` の `<li>` テキスト折返し**: tasks.md 行 1121-1122 指示通り `overflow-wrap: anywhere;` を `<li>` 単位で指定。長い URL（クエリ文字列なしでも 100 文字超のニュースサイト URL は普通にある）が dialog の max-width (560px) を超えて横スクロールを発生させないようにする。`max-height: 60vh; overflow-y: auto;` は `<ul>` 側に指定して全体 scroll に集約（Req 4.7 / 5.7 全件 reachable）。
  - **`.tile::before` (top accent bar 3px) と `.item-select` の衝突回避**: `.tile::before` は `display: block; height: 3px;` で `<article>` の最上端に 3px の bar を出す。`.item-select` は `top: var(--space-2);` で 8px 下に配置するため accent bar との視覚衝突は無い。チェックボックスの `z-index: 2` で確実に bar の上にレイヤさせた。
  - **`golangci-lint` 未実行 / `node --test` の限定実行**: `golangci-lint` は本環境にバイナリ未設置（既存 Task 5〜7 と同じ）。CSS のみの変更で Go ソースには影響しないため CI で実行される検査に委ねる。`node --test static/items_bulk_selection.test.mjs static/items_bulk_actions.test.mjs` の 55 件 + `go test ./...` 全 14 パッケージ pass を確認し、CSS 変更で JS / Go 経路に退行が無いことを担保。
- 残存課題:
  - light / dark 両テーマでの目視確認は機械検証外（既存 `data-status` / `active-filter-chip` / `tag-filter-toggle` のテーマ追従パターンと同じ運用）。`--color-primary-soft` / `--bg-elevated` / `--separator-opaque` / `--shadow-md` 等の使用 token は既に dark テーマ媒体を切り替える既存 `@media (prefers-color-scheme: dark)` セクションで上書きされる構造（line 117 付近の `:root` token と対）になっているため、本セクションが両テーマで視覚区別が成立する蓋然性は高い。
  - モバイル `< 768px` で `.bulk-toolbar` がスクリーン下端に貼り付くかは `position: sticky; bottom: 0;` の標準挙動と既存 `@media (max-width: 768px)` セクション (line 2843-2863 周辺) に既存の `.toast-container { top: 52px; }` / `.items { grid-template-columns: 1fr; }` 等が並ぶ位置と整合（モバイル overlay の z-index 衝突は前述の 300 設定で回避）。bottom-sheet (`.bottom-sheet` line 2546) と layered 表示にならない（bottom-sheet は overlay 開時のみ表示、通常は hidden）。

### Iteration round 1 (PR #141 / 2026-06-27)

PR review (`<!-- idd-claude:pr-reviewer ... -->` コメント) で指摘された 2 件の AC 違反に
対応した（spec 書き換え対象の 1 件は impl PR ガード規約により対応せず、別途返信で
Architect 差し戻しを提案）。

- **Issue 1 (high) / `items_bulk_selection.js:416` `removeFromSelection()` の DOM 同期**:
  - 既存実装は内部 `Set` から id を消すだけで、`article.is-selected` / `input.item-select.checked`
    を解除していなかった。`bulk-tag` 成功 / 部分失敗時に actions モジュールが
    `removeFromSelection(succeeded)` を呼んでも一覧上は選択済みのままに見え、Req 3.4 /
    Req 5.6 / Req 5.8 と設計上の不変「DOM 上の `.item-select[checked]` と内部 Set は
    常に同期する」に違反していた。
  - 修正方針: `removeFromSelection` のループ内で `findCard(id)` を呼び、ヒットした
    article について `.is-selected` 解除と `input.item-select.checked = false` を実行する。
    DOM 不在ケース（`bulk-delete` 成功後の fade-out 完了 / fragment swap 後）は no-op
    として安全に通過する。
  - 回帰固定: `static/items_bulk_selection.test.mjs` に 2 ケース追加。
    (a) `TestRemoveFromSelectionSyncsDOM` — 3 件選択 → `removeFromSelection(['a','b'])` で
    `a` / `b` の DOM が deselect、`c` は不変。
    (b) `TestRemoveFromSelectionNoArticleIsNoop` — DOM 不在 id の安全性確認（テスト fixture
    特有の都合で初期 appendChild の queued MutationRecord を `takeRecords()` で drain する
    必要がある — endActionMutation が後から drain すると fragment-swap reset を発火して
    しまうため）。

- **Issue 3 (medium) / `items_bulk_actions.js:191` `collectFailureItem()` の id-only fallback**:
  - レスポンスの failed 詳細を DOM から再収集する設計だが、fetch pending 中にユーザーが
    タブ切替 / フィルタ / 検索クエリ / ソート / ページ送りで対象 article を fragment swap
    で消した場合、`findCardByID(id)` が null を返して `{title:null, url:null}` 経路に倒れ、
    失敗 dialog が UUID のみ表示になる。Req 4.7 / Req 5.7「失敗したアイテムをユーザーが
    特定可能な形（タイトルまたは URL を含むメッセージ）で通知する」に違反していた。
  - 修正方針: 既存「リクエスト ID スナップショット規約」（round 6）を **詳細スナップショット
    規約**（round 7）に拡張する。click ハンドラ冒頭で `snapshotItemDetails(requestIds)` を
    呼び、`Map<id, {id, title, url}>` を click 時点で固定する。`performBulkDelete` /
    `performBulkTag` の 2 関数に optional 引数 `detailsSnapshot` を追加し、`collectFailureItem`
    は snapshot を最優先参照、無ければ live DOM、それでも見つからなければ id-only fallback、
    の 3 段で解決する。bulk-tag 用 closure 変数 `currentTagDetailsSnapshot` を新設して
    dialog 経由の submit 経路でも snapshot を保持する。
  - server 側の `BulkFailureDetail` には `Title` / `URL` を追加せず、`{ItemID, Reason}` の
    2 フィールド規約を維持する（design.md「失敗 toast の表示文言」節 / Security
    Considerations 節 PII リーク防止）。snapshot 規約はクライアント側のみの拡張。
  - 回帰固定: `static/items_bulk_actions.test.mjs` に 3 ケース追加。
    (a) `TestDeleteFailureUsesSnapshotWhenCardRemovedDuringFlight` — fetch pending 中に DOM
    から cards 消失 → 500 レスポンス → 失敗 dialog に title 表示。
    (b) `TestTagFailureUsesSnapshotWhenCardRemovedDuringFlight` — bulk-tag 部分失敗
    パスでの同等検証。
    (c) `TestSnapshotItemDetailsCollectsTitleAndUrl` — `_debug.snapshotItemDetails` API の
    Map 構築検証（vm sandbox の Object prototype 差異により `deepStrictEqual` ではなく
    プロパティ単体比較する）。

- **Issue 2 (medium) / `tasks.md:794` / `:802` の仕様整合**:
  - reviewer は task 7 の `collectFailureItem` 仕様と `_Requirements:_` の整合違反を指摘
    していたが、本 impl PR は CLAUDE.md「Developer は実装 PR で `design.md` / `tasks.md` /
    `requirements.md` を書き換えない」規約に従い、spec 書き換えを行わない。
  - 代わりに上記 Issue 3 の修正で **実装側を AC に合致させる**（snapshot 経由で title/url が
    確実に提供される）。tasks.md の表現是正は Architect 差し戻し（設計 PR）に委ねる。

- **回帰ステータス**: `go test ./...` 全 14 パッケージ pass、`node --test extension/sidepanel.test.mjs
  static/*.test.mjs` 193 件 pass（既存 188 + 新規 5）。golangci-lint は本環境にバイナリ未設置の
  ため未実行（CI で実行）。

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
