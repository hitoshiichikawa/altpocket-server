# Implementation Notes — Issue #115 アクティブフィルタをメインエリア上部にチップ表示する

## 採用方針サマリ

- **メインエリア上部（検索バー直下・一覧結果の上）にアクティブフィルタチップ列を SSR で
  常時描画**する。チップ列の置き場所は `templates/items_list.html` 冒頭。
  これにより:
  - 初期表示 / SSR full-page render / フラグメント差し替え (`X-Requested-With:
    ItemsFragment`) のいずれの経路でも、URL クエリと一致したチップ列が**サーバ側で
    確定**して返される（要件 4.3 / 4.5 / 5.1 / NFR 2.1）
  - JS は「サイドバー change」「カード上タグ click」「popstate」のタイミングでも
    フラグメント差し替えを起こすため、JS の責務は **chip 列を弄る独立 listener を
    持つだけ**で済む（フラグメント差し替え時は HTML が丸ごと入れ替わるので JS が
    chip DOM を再構築する必要はない）
- **チップ自体は `<a>` で SSR フォールバック可能にする**。href には「対象タグを取り除いた
  正準形式 URL」が入る。JS が無効でもクリックでページ遷移し、サーバ側で該当タグ抜きの
  一覧と chip 列を返せる（NFR 2.1 / 要件 5.1）。JS あり時は click を `preventDefault` して
  history.pushState + フラグメント取得に切り替える（NFR 1.1 / 1.2）
- **「すべてクリア」も `<a href>` で SSR フォールバック可能にする**。href は
  「タグ絞り込みパラメータをすべて取り除いた URL」
- **JS 共有規約**: Issue #117 で導入された `[data-items-region]` 上の
  `__itemsFragmentInflight` AbortController slot を共有する。チップ click による
  フラグメント取得も、検索 debounce 由来 / カード上タグクリック由来の保留 fetch を
  abort して race を防ぐ
- **チップ DOM 契約**:
  - 外側 `<a class="chip active-filter-chip" data-active-filter-chip
    data-tag-normalized="<norm>" href="<解除後 URL>" role="button"
    aria-label="フィルタ解除: <表示名>">`
  - 内側に `<span class="active-filter-chip-label">{{表示名}}</span>` +
    `<span class="active-filter-chip-x" aria-hidden="true">×</span>`
  - SSR の `aria-label` で個別解除の意味を支援技術に伝える（要件 6.4）
- **「すべてクリア」DOM**: `<a class="active-filter-clear-all" data-active-filter-clear-all
  href="<タグなし URL>" role="button" aria-label="すべてのフィルタを解除">`

## Open Questions の解釈

### (a) チップ列の位置 → デスクトップ・モバイル共通で「検索バー直下・一覧結果の上」

要件 1.1 が「メインエリア上部（検索バー直下・一覧結果の上）」と明示しており、NFR 3.2 も
「モバイル幅とデスクトップ幅のいずれにおいてもアクティブフィルタチップ列をメインエリア上部
の同一位置に表示する」と固定している。`items_list.html` の冒頭に配置することで、デスクトップ
（`<section class="items">` の中、モバイル（filter-toggle-bar の下）どちらでも自動的に
同一位置に出る。

### (b) 履歴粒度 → **pushState を採用**（PM 観点と一致）

チップ操作・「すべてクリア」とも `history.pushState` で履歴を残す。理由:
- Issue #117 のタグクリック（追加）も pushState を採用済み。逆向きの「解除」だけ
  replaceState にすると一貫性が崩れる
- ユーザは複数チップを連続解除した後、「戻る」で 1 個前の絞り込みに戻りたいと期待する
  ことが多い（PM 観点コメント）
- replaceState で履歴を増やさないと、ユーザが「絞り込みを徐々に外して全体一覧へ戻った後、
  戻るで意図せず別画面に遷移する」事故が起きる

### (c) SSR vs CSR → **SSR で初期チップを描画**（NFR 2.1 / 要件 4.5 必須化）

- NFR 2.1 が JS 無効環境でのサイドバーフォーム送信互換を要求している。SSR でチップ列が
  描画されていれば、JS 無効環境でもチップを `<a>` として残せ、クリックでサーバ側に
  「解除後 URL」を送れる
- 要件 4.5 が「URL を直接開いたとき初期表示時点で chip 列が URL クエリと一致する」を要求。
  SSR が一番自然
- フラグメント差し替え経路（`X-Requested-With: ItemsFragment`）でもサーバ側で chip 列を
  含めて返すことで、JS は chip 列の DOM を弄らずに済む（責務分離）

### (d) 取得経路 → **フラグメント取得経路 (`X-Requested-With: ItemsFragment`)**

- NFR 1.2 (ちらつき防止)・要件 4.3（フラグメント差し替え後の整合）を満たすために必須
- Issue #117 と同じ規約 (AbortController 共有・X-Requested-With ヘッダ・region.innerHTML
  差し替え) を踏襲することで、cross-module race も既存仕組みで防げる

## 各 AC への対応マップ

| AC | 実装場所 | 概要 |
|---|---|---|
| 1.1 | `items_list.html` | `{{if .ActiveTagFilters}}` で 1 件以上ある時のみ chip 列を描画 |
| 1.2 | `items_list.html` | `ActiveTagFilters` 0 件時は `<div class="active-filters">` 自体を出さない |
| 1.3 | `items_list.html` | 各 chip に `{{.Name}}`（表示名）を出力 |
| 1.4 | `items_list.html` | 各 chip 内に解除「×」表示。chip 全体が解除ボタンを兼ねる |
| 1.5 | `server.go` `handleUIItems` | `ActiveTagFilters` は SelectedTags と同一集合（順序はサーバ側 `Tags` facet の登場順を踏襲。重複防止のため `parseTagFilters` 結果を canonical とする） |
| 2.1 | `static/items_active_filters.js` `commitRemove` | chip クリックで対象 normalized name を URL から削除 |
| 2.2 | `items_active_filters.js` `refreshFragment` | フラグメント取得で一覧を新条件に更新 |
| 2.3 | `items_active_filters.js` → `items_tags.js syncControls` 共有 | チェックボックスを新状態に合わせる |
| 2.4 | `items_active_filters.js` → `syncControls` 共有 | カード上タグ button の `aria-pressed` / `is-selected` 更新 |
| 2.5 | `items_active_filters.js` `buildRemovedURL` | 最後 1 件解除で `?tag=` パラメータ自体を URL から落とす |
| 2.6 | `items_active_filters.js` `commitRemove` の `history.pushState` | URL を新条件に更新 |
| 3.1 | `items_list.html` | chip 列内に「すべてクリア」`<a>` を 1 件以上の時のみ表示 |
| 3.2 | `items_active_filters.js` `commitClearAll` | キーボード Enter / Space / マウスクリックで全解除 |
| 3.3 | `items_active_filters.js` `refreshFragment` | 全解除後に一覧フェッチ |
| 3.4 | `items_active_filters.js` `syncControls(空配列)` | サイドバー全 checkbox を未選択に |
| 3.5 | 同上 | カード上タグ全 button から `is-selected` 解除 |
| 3.6 | `items_active_filters.js` `commitClearAll` の `buildClearedURL` | URL から `tag` / `tags` 両形式を消す |
| 4.1 | `items_tags.js` 既存 + フラグメント取得経路 | サイドバー change（auto-submit 経由 fullpage / ボトムシート Apply）でも、最終 SSR 再描画で chip 列が新条件と一致 |
| 4.2 | カード上タグ click → 既存 `items_tags.js` フラグメント取得 → サーバ側で `ActiveTagFilters` 再構築 | フラグメント差し替え経由で chip 列も更新 |
| 4.3 | フラグメント差し替えに chip 列を含める | フラグメント取得後の `region.innerHTML` 差し替えで chip 列も書き換わる |
| 4.4 | `items_active_filters.js` `onPopState` | popstate で `refreshFragment(location.href)` を呼ぶ。サーバが新 URL に応じた chip 列を返す |
| 4.5 | SSR full-page render の `handleUIItems` | URL から `parseTagFilters` → `ActiveTagFilters` を構築 |
| 5.1 | `buildRemovedURL` / `buildClearedURL` | 正準 `?tag=<normalized>` 繰り返しのみを生成し、旧 `?tags=` 複数形は全削除 |
| 5.2 | `buildRemovedURL` / `buildClearedURL` | `q` / `sort` / `per_page` / `page` を保持。`tag` / `tags` のみ操作 |
| 5.3 | `buildRemovedURL` / `buildClearedURL` | 配列空時に `searchParams.delete('tag')` / `delete('tags')` を呼ぶ |
| 5.4 | サーバ既存 `parseTagFilters` を流用 | 解釈ロジックは変更なし。既存 URL は引き続き同じ結果 |
| 6.1 | `<a href>` チップなのでネイティブにフォーカス可能 | `tabindex` 操作不要 |
| 6.2 | `<a>` click が Enter で発火する HTML 仕様 + `commitRemove` 共通経路 | Enter で同じ挙動 |
| 6.3 | 同上 + 「すべてクリア」も `<a>` | Enter で同じ挙動 |
| 6.4 | `<a aria-label="フィルタ解除: <表示名>">` | 支援技術向け命名 |
| 6.5 | `<a aria-label="すべてのフィルタを解除">` | 支援技術向け命名 |
| NFR 1.1 | `commitRemove` 内で `syncControls(tags)` を即時呼び出し、`refreshFragment` を非同期 | DOM 更新は 300ms 待たずに即時 |
| NFR 1.2 | フラグメント取得経路 + AbortController slot 共有 | innerHTML 差し替えで一気に切り替えなのでちらつかない |
| NFR 1.3 | AbortController で前段破棄 | 最後の操作の結果のみ表示 |
| NFR 2.1 | `<a href>` SSR フォールバック | JS 無効でも click でフルページ遷移 |
| NFR 2.2 | parseTagFilters 既存挙動を保持 | 既存 URL の解釈不変 |
| NFR 2.3 | `items_tags.js` の click 経路は触らない | カード上タグの動作は完全に維持 |
| NFR 3.1 | `static/style.css` の既存 `.chip` / `.tag-chip` トークンを再利用 | 新規変数を増やさず既存に揃える |
| NFR 3.2 | `items_list.html` 冒頭に配置 = デスクトップ・モバイル同一位置 | media query 不要 |

## 変更ファイル一覧（予定）

| ファイル | 種類 | 役割 |
|---|---|---|
| `internal/server/server.go` | 変更 | `handleUIItems` に `ActiveTagFilters` を構築する箇所を追加。フラグメント経路でも `Tags` facet を取得して `ActiveTagFilters` を計算する（チップ列の表示名解決のため） |
| `internal/server/server_test.go` | 変更 | `buildActiveTagFilters` ヘルパー関数の単体テストを追加 |
| `templates/items_list.html` | 変更 | 冒頭にアクティブフィルタチップ列を追加（SSR + フラグメント差し替えの両方でレンダリングされる） |
| `static/style.css` | 変更 | `.active-filters` / `.active-filter-chip` / `.active-filter-clear-all` の配色・余白・hover・focus・モーションを既存トークンで定義 |
| `static/items_active_filters.js` | 新規 | チップ click と「すべてクリア」click を捕捉し、URL 更新 + フラグメント取得 + サイドバー / カード UI 同期を行う。AbortController slot 共有 |
| `static/items_active_filters.test.mjs` | 新規 | 上記ロジックの単体テスト (node:test + vm) |
| `templates/items.html` | 変更 | `<script src="/static/items_active_filters.js">` 追加 |
| `internal/ui/render_test.go` | 変更 | チップ列の SSR レンダリングを担保するテストを追加 |

## 確認事項（PR 本文に記載するレビュワー判断ポイント）

- (a) **履歴粒度に pushState を採用**: 「戻る」で解除前の絞り込み状態に戻れる。
  replaceState で履歴を増やさない設計を希望する場合は差し戻し
- (b) **フラグメント経路でも `Tags` facet クエリを叩かない設計**: チップの表示名は
  「Tags facet (full-page) → items の Tags (fragment) → normalized name フォールバック」の
  優先順位で解決する。fragment 経路では追加 DB クエリを発生させない（既存の skip 規約を維持）
- (c) **「すべてクリア」のアクセシブル命名は日本語**: `aria-label="すべてのフィルタを解除"`。
  「Clear all」等の英語表記を希望する場合は差し戻し
- (d) **チップ全体が解除ボタンを兼ねる設計**: 要件 1.4 の「各チップに、そのフィルタを
  解除するための操作要素（解除ボタン相当）を含める」を、チップ全体（`<a>`）に解除動作を
  紐付ける形で実装。視覚的に右端に「×」を表示するが、これは `aria-hidden="true"` で
  支援技術には独立した操作要素として認識させない（chip 1 つあたり 1 つのアクセシブル名
  に集約）。視覚的「×」を独立した button にしたほうが UX として良いと判断する場合は差し戻し
- (e) **`page` パラメータのリセット**: チップ操作・「すべてクリア」のいずれも、新しい
  絞り込みで `page` クエリパラメータを削除する（リセットして page=1 に戻す）。理由は
  「新しい絞り込み結果は元より少ない件数になることが多く、`page=3` のまま遷移すると空ページに
  着地するリスク」があるため。Issue #117 のタグ click では `pageURL` 経由で page を保つ仕様だが、
  本 Issue では絞り込み解除なので page リセットが自然と判断。明示確認したい
- (f) **`r.URL` を直接 `buildTagRemovedURL` に渡す**: query parameter のみ操作するため
  scheme / host / path は触らない。テストでは `/ui/items?...` だけで動くことを確認済み

## 実装サマリ

### 変更ファイル一覧（実装後の確定）

| ファイル | 種類 | 概要 |
|---|---|---|
| `internal/server/server.go` | 変更 | `ActiveTagFilter` 型と `buildActiveTagFilters` / `buildTagRemovedURL` / `buildClearAllTagsURL` / `cloneURL` ヘルパー追加。`handleUIItems` の data に `ActiveTagFilters` / `ClearAllTagsURL` を追加 |
| `internal/server/server_test.go` | 変更 | 上記 3 関数の単体テスト追加（合計 16 ケース） |
| `templates/items_list.html` | 変更 | 冒頭にアクティブフィルタチップ列の SSR 描画を追加（`{{if .ActiveTagFilters}}...{{end}}` で 0 件時は領域自体を出さない） |
| `templates/items.html` | 変更 | `<script src="/static/items_active_filters.js">` を defer で読み込み追加 |
| `static/style.css` | 変更 | `.active-filters` / `.active-filter-chip` / `.active-filter-clear-all` のスタイル追加。既存トークンのみ使用 |
| `static/items_active_filters.js` | 新規 | チップ click / 「すべてクリア」click / popstate のハンドリング。AbortController slot 共有 |
| `static/items_active_filters.test.mjs` | 新規 | 上記の単体テスト 15 件 |
| `internal/ui/render_test.go` | 変更 | `TestActiveFiltersRendering` (8 ケース) + `TestActiveFiltersFragmentRendering` 追加 |

### コミット一覧

| commit | 概要 |
|---|---|
| 1f69322 | `feat(items): アクティブフィルタチップ列を SSR で描画する` (サーバ + テンプレ + テスト) |
| 5ee467f | `feat(items): アクティブフィルタチップに JS クリック解除を付与する` (JS + CSS + テスト) |

### テスト結果

#### Go テスト (`go test ./...`)

```
ok  	altpocket/cmd/worker
ok  	altpocket/internal/auth
ok  	altpocket/internal/config
ok  	altpocket/internal/crypto
ok  	altpocket/internal/fetcher
ok  	altpocket/internal/mcpserver
ok  	altpocket/internal/ratelimit
ok  	altpocket/internal/server  (extension_contract_test.go 含む)
ok  	altpocket/internal/store
ok  	altpocket/internal/tag
ok  	altpocket/internal/ui
ok  	altpocket/internal/urlnorm
```

新規追加テスト:
- `TestBuildClearAllTagsURL` (3 ケース)
- `TestBuildTagRemovedURL` (5 ケース)
- `TestBuildActiveTagFilters` (7 ケース)
- `TestActiveFiltersRendering` (8 ケース)
- `TestActiveFiltersFragmentRendering` (1 ケース)

#### Go vet (`go vet ./...`)

エラーなし（出力なし）。

#### gofmt (`gofmt -l .`)

本実装で編集したファイルは差分なし。`internal/server/server.go` の line 677 周辺に
**Pre-existing** な gofmt 差分（既存 `Title *string` のアラインメント）が残るが、これは
本 Issue で触っていない範囲なので、Issue #117 の impl-notes と同じく PR 範囲外とする。

#### JS テスト (`node --test static/items_active_filters.test.mjs`)

```
1..15
# tests 15
# pass 15
# fail 0
```

#### 既存 JS テスト (`node --test static/items_tags.test.mjs
static/items_search.test.mjs static/items_fragment_race.test.mjs`)

```
1..39
# tests 39
# pass 39
# fail 0
```

#### 拡張機能テスト (`node --test extension/sidepanel.test.mjs`)

```
1..30
# tests 30
# pass 30
# fail 0
```

（拡張機能の `extension_contract_test.go` も Go テストとして pass を確認済み）

### 受入基準（AC）達成確認

| AC ID | 担保箇所 |
|---|---|
| 1.1 | `TestActiveFiltersRendering/Req_1.1_...` + `TestActiveFiltersRendering/Req_1.1_(placement)` |
| 1.2 | `TestActiveFiltersRendering/Req_1.2:_zero_active_filters_does_not_render_chip_row` |
| 1.3 | `TestActiveFiltersRendering/Req_1.1_/_1.3_/_1.4` |
| 1.4 | 同上 |
| 1.5 | `TestBuildActiveTagFilters/Req_1.5:_chip_order_matches_the_tagFilters_input_order` + テンプレート |
| 2.1 | `items_active_filters.test.mjs / 要件 2.1 / 2.2 / 2.6` + `TestBuildTagRemovedURL` |
| 2.2 | `items_active_filters.test.mjs / 要件 2.1 / 2.2 / 2.6`（フラグメント取得を検証） |
| 2.3 | `items_active_filters.test.mjs / 要件 2.3 / 3.4` |
| 2.4 | `items_active_filters.test.mjs / 要件 2.4 / 3.5` |
| 2.5 | `items_active_filters.test.mjs / 要件 2.5 / 3.6 / 5.3` + `TestBuildTagRemovedURL/last_tag_removed_strips_tag_parameter_entirely` |
| 2.6 | `items_active_filters.test.mjs / 要件 2.6 (履歴粒度): pushState を使い replaceState を使わない` |
| 3.1 | `TestActiveFiltersRendering/Req_3.1:_clear-all_control_is_rendered_when_there_is_at_least_one_chip` |
| 3.2 | `items_active_filters.test.mjs / 要件 3.2 / 3.3 / 3.6` |
| 3.3 | 同上（フラグメント取得が走ることを検証） |
| 3.4 | `items_active_filters.test.mjs / 要件 3.4 / 3.5 (clear all)` |
| 3.5 | 同上 |
| 3.6 | `TestBuildClearAllTagsURL/removes_tag_and_tags_parameters` + `items_active_filters.test.mjs / 要件 3.6 (履歴粒度)` |
| 4.1 | サイドバー auto-submit はフルページ遷移で SSR 再描画される設計。サーバ側テスト `TestActiveFiltersRendering` で SSR が ActiveTagFilters を反映することを担保 |
| 4.2 | `items_tags.js` 既存のフラグメント取得経路で再描画。fragment 経路でも `buildActiveTagFilters` が呼ばれることを `TestActiveFiltersFragmentRendering` で担保 |
| 4.3 | `TestActiveFiltersFragmentRendering`（フラグメント側にもチップ列が含まれること） |
| 4.4 | `items_active_filters.test.mjs / 要件 4.4: popstate で新しい URL に応じたフラグメント取得が走る` |
| 4.5 | SSR 経路でチップ列が初期描画される設計。`TestActiveFiltersRendering` 全体で担保 |
| 5.1 | `TestBuildTagRemovedURL/legacy_?tags=csv_is_migrated_to_canonical_?tag=_repetition` + `items_active_filters.test.mjs / 要件 5.1 / 5.2` |
| 5.2 | `TestBuildTagRemovedURL/preserves_q_/_sort_/_per_page_..._and_resets_page` + `TestBuildClearAllTagsURL/preserves_other_query_parameters_except_page` |
| 5.3 | `TestBuildClearAllTagsURL/removes_tag_and_tags_parameters` + `TestBuildTagRemovedURL/last_tag_removed_strips_tag_parameter_entirely` |
| 5.4 | サーバ側既存 `parseTagFilters` を流用しており解釈ロジック不変。`TestParseTagFilters` (既存) でカバー |
| 6.1 | `<a href>` 要素なのでネイティブにキーボードフォーカス可能（HTML 仕様） |
| 6.2 | `items_active_filters.test.mjs / 要件 6.2: <a> なので Enter は click にディスパッチされる前提` |
| 6.3 | 同上（「すべてクリア」も `<a>`） |
| 6.4 | `TestActiveFiltersRendering/Req_6.4:_chip_carries_aria-label` |
| 6.5 | `TestActiveFiltersRendering/Req_6.5:_clear-all_control_carries_an_accessible_name` |
| NFR 1.1 | `items_active_filters.test.mjs / NFR 1.1: チップクリックで pushState + UI 同期が fetch 完了を待たずに即時実行される` |
| NFR 1.2 | `items_active_filters.test.mjs / NFR 1.2 / 1.3: 連続チップクリックで前段の保留 fetch が AbortController で破棄される` + region.innerHTML 一括差し替えによるちらつき防止（フラグメント取得経路の既存規約） |
| NFR 1.3 | 同上 + `AbortController slot を items_tags.js / items_search.js と共有する` |
| NFR 2.1 | チップ・clear-all を `<a href>` で実装 + SSR 描画。`TestActiveFiltersRendering/Req_5.1_/_NFR_2.1:_chip_is_an_<a_href>_pointing_at_the_RemoveURL` で担保 |
| NFR 2.2 | サーバ側 `parseTagFilters` 不変。`TestParseTagFilters` (既存) でカバー |
| NFR 2.3 | `items_tags.js` の処理経路に変更なし。`items_tags.test.mjs` の全 11 ケースが引き続き pass することで担保 |
| NFR 3.1 | `style.css` で既存トークン (`--color-primary-soft` / `--color-primary` / `--color-danger` / `--motion-fast` / `--ease-default` / `--space-*` / `--radius-sm` / `--type-caption-*-size`) のみ使用。新規変数を追加せず |
| NFR 3.2 | チップ列を `items_list.html` 冒頭に配置することで、デスクトップ・モバイル両幅で同一位置（filter-toggle-bar 直下 / 検索バー直下）に表示される設計。media query 不要 |

## Round-3 codex review fix（full-page 経路の 0 件絞り込み表示名解決）

codex レビュー指摘（medium・`internal/server/server.go:765`）への対応:

- **症状**: full-page 初期表示（`fragmentOnly=false`）では `tagsForLookup` に
  `ListTagsWithCountFiltered`（絞り込み済み facet）の結果だけを入れていた。タグ AND 条件が
  0 件になる URL を直開きすると facet が空になり、`buildActiveTagFilters` の正規化名 fallback
  （`if name == ""`）に落ちてチップが**正規化名**で表示されていた。これは AC 1.3（元の表示名を
  表示・`requirements.md:30`）と AC 4.5（URL 直接入力時に一致表示・`requirements.md:70`）に違反する。
- **修正**: full-page・fragment いずれの経路でも、タグ絞り込みがあるときは
  `TagsByNormalizedNames(tagFilters)` で表示名を直接解決し、その結果を facet に**マージ**する
  （新ヘルパ `mergeTagDisplaySources`）。facet が空の 0 件絞り込みでも表示名が解決され、正規化名
  落ちを解消する。facet が非空のときは facet（canonical casing）を優先（earlier-source-wins）。
  fragment 経路は従来どおり TagsByNormalizedNames を使うため挙動不変。
- **追加テスト**:
  - `internal/server/server_test.go`: `TestFullPageZeroResultResolvesDisplayName`（0 件絞り込みで
    facet 空 + 直接 lookup → 元の表示名を解決する純粋関数レベル回帰テスト）/ `TestMergeTagDisplaySources`
  - `internal/server/items_active_filters_integration_test.go`（`-tags=integration` / `TEST_DATABASE_URL`
    gated）: 実 DB に対し full-page 経路のデータパスを再現し、0 件 AND 絞り込み URL 直開きでも
    チップが元の表示名を解決することを担保。

STATUS: complete
