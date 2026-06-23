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

- (b) 履歴粒度: pushState を採用。replaceState の方が良いと判断する場合は要差し戻し
- (c) フラグメント経路で `Tags` facet クエリを毎回叩く負荷増: 既存実装では fragment 経路で
  `ListTagsWithCountFiltered` を skip していたが、本実装ではチップ列の表示名解決のため
  fragment 経路でも呼ぶ必要がある。表示名解決のためだけならフラグメント側は SelectedTags
  ＋ ActiveTagFilters の 2 配列で済むが、最も自然な実装としては `Tags` facet からの
  filter を採用した
- (d) 「すべてクリア」のアクセシブル命名: `aria-label="すべてのフィルタを解除"` を採用。
  「Clear all」等の英語混在を希望する場合は要差し戻し
