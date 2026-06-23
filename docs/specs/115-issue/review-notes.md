# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-06-23T00:00:00Z -->

## Reviewed Scope

- Branch: claude/issue-115-impl-issue
- HEAD commit: 8af27c2ee4a4c93063768e7fcc294b83e448d48c
- Compared to: develop..HEAD
- Mode: design-less impl（`docs/specs/115-issue/` 配下に `design.md` / `tasks.md` は存在しない。
  `_Boundary:_` アノテーションは存在しないため、変更ファイル群の Items UI 機能境界での
  scope 妥当性で判定した）
- Feature Flag Protocol: opt-out（`CLAUDE.md` の `## Feature Flag Protocol` 節を確認。
  flag 観点の細目チェックは適用しない）

差分構成（`git diff --stat develop..HEAD`）:

```
 docs/specs/115-issue/impl-notes.md   | +282
 docs/specs/115-issue/requirements.md | +131
 internal/server/server.go            | +144 -17 (handleUIItems + helpers)
 internal/server/server_test.go       | +231 (TestBuildClearAllTagsURL / TestBuildTagRemovedURL / TestBuildActiveTagFilters)
 internal/ui/render_test.go           | +219 (TestActiveFiltersRendering / TestActiveFiltersFragmentRendering)
 static/items_active_filters.js       | +233 (新規モジュール)
 static/items_active_filters.test.mjs | +631 (新規テスト 15 件)
 static/style.css                     | +110 (Active Filter Chips スタイル)
 templates/items.html                 | +1   (script include)
 templates/items_list.html            | +34  (チップ列の SSR テンプレート)
```

## Verified Requirements

### Requirement 1: アクティブフィルタチップの可視化

- **1.1** — `templates/items_list.html` 冒頭で `{{if .ActiveTagFilters}}` ガード付きで
  チップ列を出力。`internal/ui/render_test.go::TestActiveFiltersRendering` の
  `Req 1.1 (placement)` でチップ列がアイテムカードより前に出力されることを検証
- **1.2** — 同テンプレートの `{{if .ActiveTagFilters}}` で 0 件時はマーカ自体が
  描画されない。`TestActiveFiltersRendering` の `Req 1.2: zero active filters does not
  render chip row` で検証
- **1.3** — チップ内 `<span class="active-filter-chip-label">{{.Name}}</span>` が
  display name を出力。`buildActiveTagFilters` (server.go:1497) で facet → items → norm の
  優先順で display name を解決。`TestBuildActiveTagFilters` の各ケースおよび
  `TestActiveFiltersRendering` の `Req 1.1 / 1.3 / 1.4` で検証
- **1.4** — チップ自体が `<a class="chip active-filter-chip" href="<RemoveURL>">` で
  解除コントロールを兼ねる + 視覚的 `×` を内包。`TestActiveFiltersRendering` の
  `Req 1.1 / 1.3 / 1.4` で `data-active-filter-chip` の存在を検証
- **1.5** — `buildActiveTagFilters` (server.go:1517-1530) が `tagFilters`（=サーバ側
  `parseTagFilters` の正準集合）を順序保持で 1:1 マップ。
  `TestBuildActiveTagFilters/Req 1.5: chip order matches the tagFilters input order` で検証

### Requirement 2: チップによる個別フィルタ解除

- **2.1** — `items_active_filters.js` の `onDocumentClick` → `commit(href)` 経路で
  チップ click から対象タグを URL から削除。実 URL 生成は SSR 側 `buildTagRemovedURL` が
  担当。`items_active_filters.test.mjs / 要件 2.1 / 2.2 / 2.6` および
  `TestBuildTagRemovedURL/removes one tag and keeps the others in canonical form` で検証
- **2.2** — `commit()` が `refreshFragment(targetHref)` を呼びフラグメント再取得。
  `items_active_filters.test.mjs / 要件 2.1 / 2.2 / 2.6` で `X-Requested-With:
  ItemsFragment` ヘッダ送信と `region.innerHTML` 差し替えを検証
- **2.3** — `syncControls(selectedTags)` がサイドバー `input[type="checkbox"][name="tag"]`
  を新条件に同期。`items_active_filters.test.mjs / 要件 2.3 / 3.4` で検証
- **2.4** — 同 `syncControls` がカード上 `[data-tag-filter-toggle]` の `aria-pressed`
  と `is-selected` クラスを更新。`items_active_filters.test.mjs / 要件 2.4 / 3.5` で検証
- **2.5** — `buildTagRemovedURL` (server.go:1539) は残数 0 件で `tag` / `tags` 両形式を
  `q.Del()` でクリア。`TestBuildTagRemovedURL/last tag removed strips tag parameter
  entirely (Req 2.5 / 5.3)` および `items_active_filters.test.mjs / 要件 2.5 / 3.6 /
  5.3` で検証
- **2.6** — `commit()` が `history.pushState({source:'items_active_filters'}, '',
  targetHref)`。`items_active_filters.test.mjs / 要件 2.6 (履歴粒度): チップクリックは
  pushState を使い、replaceState を使わない` で `pushState=1, replaceState=0` を検証

### Requirement 3: 「すべてクリア」による一括解除

- **3.1** — `items_list.html` の `{{if .ActiveTagFilters}}` 内に
  `<a class="active-filter-clear-all" href="{{.ClearAllTagsURL}}">` が同居（1 件以上
  あるときだけ描画）。`TestActiveFiltersRendering/Req 3.1: clear-all control is
  rendered when there is at least one chip` で検証
- **3.2** — `onDocumentClick` 中の `clearAll` 分岐が `commit(clearAll.getAttribute('href'))` を呼ぶ。
  `items_active_filters.test.mjs / 要件 3.2 / 3.3 / 3.6` で検証
- **3.3** — 同 `commit()` 経由で `refreshFragment` が走り一覧再描画。同テストで検証
- **3.4** / **3.5** — `commit()` 内の `syncControls(readURLTags(targetHref))` で
  全 checkbox / 全カード button が未選択化。
  `items_active_filters.test.mjs / 要件 3.4 / 3.5: 「すべてクリア」でサイドバー
  checkbox とカード上タグの全選択が解除される` で検証
- **3.6** — `buildClearAllTagsURL` (server.go:1697) が `tag` / `tags` を `q.Del()` し、
  `page` を含むタグ以外の既存クエリは保持する（Req 5.2 / round-2 iteration で AC 整合のため
  `q.Del("page")` を撤去）。`TestBuildClearAllTagsURL/removes tag and tags parameters` で検証

### Requirement 4: 既存フィルタ機構との状態同期

- **4.1** — サイドバー checkbox 操作は既存 `items_tags.js` の auto-submit / Apply 経由で
  fragment 取得（既存挙動）。fragment レスポンスは `templates/items_list.html` を再
  レンダリングするため、チップ列はサーバ側 `buildActiveTagFilters` の結果と必ず一致する。
  `TestActiveFiltersFragmentRendering` で fragment 経路にチップ列が含まれることを検証
- **4.2** — カード上タグ click は既存 `items_tags.js` の fragment 取得経路を使う。同様に
  `templates/items_list.html` を再レンダリングするため自動的にチップ列が同期。
  `TestActiveFiltersFragmentRendering` で検証
- **4.3** — `handleUIItems` が `fragmentOnly=true` 経路でも `ActiveTagFilters` /
  `ClearAllTagsURL` を data に詰め、fragment テンプレートに `items_list.html` を含めて
  返す。`TestActiveFiltersFragmentRendering` で fragment レスポンス内に
  `data-active-filters` / `data-tag-normalized="go"` が含まれることを検証
- **4.4** — `items_active_filters.js` の `win.addEventListener('popstate', onPopState)` で
  `refreshFragment(location.href)` を呼ぶ。
  `items_active_filters.test.mjs / 要件 4.4: popstate で新しい URL に応じたフラグメント
  取得が走る` で検証
- **4.5** — `handleUIItems` の SSR full-page 経路で `tagFilters := parseTagFilters(r.URL)` →
  `buildActiveTagFilters(...)` を呼ぶため、初回 URL アクセスでチップ列が出力される。
  `TestActiveFiltersRendering` 全 8 ケースで検証

### Requirement 5: 既存 URL クエリ形式との互換性

- **5.1** — `buildTagRemovedURL` / `buildClearAllTagsURL` が常に `q.Del("tag")` /
  `q.Del("tags")` で旧形式をクリアしてから `q.Add("tag", ...)` で正準 `?tag=<norm>`
  繰り返しを再構築。`TestBuildTagRemovedURL/legacy ?tags=csv is migrated to canonical
  ?tag= repetition (Req 5.1)` で検証
- **5.2** — 両 helper は `q.Del()` 対象を `tag` / `tags` のみに限定し、`page` を含む
  タグ以外の既存クエリ（検索キーワード / 並び順 / 1 ページ件数 / ページ番号）を保持する。
  `TestBuildTagRemovedURL/preserves q / sort / per_page / page (Req 5.2)` および
  `TestBuildClearAllTagsURL/preserves other query parameters including page (Req 5.2)` で検証
  （round-2 iteration で AC 5.2 「ページ番号などを保持する」に整合させるため、初期の
  `q.Del("page")` を撤去し対応テストの期待も「page も保持される」に修正した）
- **5.3** — `buildTagRemovedURL` は最後の 1 件削除で `q.Add` が呼ばれず `tag` パラメータが
  消える。`TestBuildTagRemovedURL/last tag removed strips tag parameter entirely
  (Req 2.5 / 5.3)` で検証
- **5.4** — サーバ側 `parseTagFilters` ロジックを変更していない（diff にも該当箇所なし）。
  既存 URL の解釈は不変。impl-notes でも明示

### Requirement 6: キーボード・アクセシビリティ対応

- **6.1** — チップ・clear-all とも `<a href>` でネイティブにフォーカス可能（HTML 仕様）
- **6.2** / **6.3** — `<a href>` の Enter は HTML 仕様で click にディスパッチされる。
  `items_active_filters.test.mjs / 要件 6.2: <a> なので Enter は click にディスパッチ
  される (JS は preventDefault でフルページ遷移を抑止)` で検証
- **6.4** — 各チップに `aria-label="フィルタ解除: {{.Name}}"`。
  `TestActiveFiltersRendering/Req 6.4: chip carries aria-label with both the tag display
  name and the 'unset' intent` で `aria-label="フィルタ解除: Go"` の出力を検証
- **6.5** — clear-all に `aria-label="すべてのフィルタを解除"`。
  `TestActiveFiltersRendering/Req 6.5: clear-all control carries an accessible name
  describing 'unset all'` で検証

### Non-Functional Requirements

- **NFR 1.1** — `commit()` 内で `history.pushState` と `syncControls` が同期実行され、
  `refreshFragment` は `void` で非同期発火。
  `items_active_filters.test.mjs / NFR 1.1: チップクリックで pushState + UI 同期が
  fetch 完了を待たずに即時実行される` で pending fetch 下で pushState / 同期が完了することを検証
- **NFR 1.2** / **NFR 1.3** — `refreshFragment` 内で前段の `coord.ctrl.abort()` を呼んでから
  新 controller を `coord.ctrl` にセット。
  `items_active_filters.test.mjs / NFR 1.2 / 1.3: 連続チップクリックで前段の保留 fetch が
  AbortController で破棄される` で検証
- **NFR 2.1** — チップ・clear-all とも `<a href="<解除後 URL>">` で SSR フォールバック。
  `TestActiveFiltersRendering/Req 5.1 / NFR 2.1: chip is an <a href> pointing at the
  RemoveURL` で検証
- **NFR 2.2** — `parseTagFilters` 不変、`buildTagRemovedURL` / `buildClearAllTagsURL` は
  生成側のみで解釈側は触らず
- **NFR 2.3** — `items_tags.js` / カード上タグ click ロジックに変更なし（diff に該当なし）
- **NFR 3.1** — `static/style.css` の追加トークンは既存 `--color-primary-soft` /
  `--color-primary` / `--color-danger` / `--motion-fast` / `--ease-default` /
  `--space-*` / `--radius-sm` / `--type-caption-*-size` のみ
- **NFR 3.2** — チップ列を `items_list.html` 冒頭に置くことでデスクトップ・モバイル両方で
  同一位置になる（media query なし）

### 境界（Boundary）

design-less impl のため `tasks.md` の `_Boundary:_` 宣言は不在。本 PR の差分は以下のいずれも
Items UI 機能（`/ui/items` 画面のアクティブフィルタチップ列）に直接寄与するもので、
スコープ逸脱は検出されない:

- `internal/server/server.go` — `handleUIItems` + 新規 helper（`ActiveTagFilter` /
  `buildActiveTagFilters` / `buildTagRemovedURL` / `buildClearAllTagsURL` / `cloneURL`）
- `internal/server/server_test.go` / `internal/ui/render_test.go` — 上記の単体テスト
- `templates/items.html` / `templates/items_list.html` — SSR テンプレート
- `static/style.css` / `static/items_active_filters.js` / `static/items_active_filters.test.mjs`
  — 新規 CSS + JS モジュール + 単体テスト
- `docs/specs/115-issue/{requirements,impl-notes}.md` — spec 成果物

`parseTagFilters` などサーバ側の解釈ロジック、`items_tags.js` / `items_search.js` などの
既存 JS、`extension_contract_test.go` などの拡張機能契約には触れていない。

### テスト実行（impl-notes.md 記載）

- `go test ./...` — all green（新規追加 5 件 = 全 24 ケース合格）
- `go vet ./...` — エラーなし
- `node --test static/items_active_filters.test.mjs` — 15/15 pass
- 既存 JS テスト (`items_tags.test.mjs` / `items_search.test.mjs` /
  `items_fragment_race.test.mjs`) — 39/39 pass
- 拡張機能テスト (`extension/sidepanel.test.mjs`) — 30/30 pass

reviewer 側でテスト再実行は impl-notes に詳細結果があるため省略。

## Findings

なし

## Summary

Issue #115 の AC（Requirement 1〜6 / NFR 1〜3 / 全 28 項目）すべてに対し、`buildActiveTagFilters`
/ `buildTagRemovedURL` / `buildClearAllTagsURL` の SSR helper・`templates/items_list.html` の
チップ列テンプレート・`static/items_active_filters.js` のクライアント側ハンドラがそれぞれ
担当し、合計 38 個のテストケース（Go 24 + JS 15、`render_test.go` 8、`server_test.go` 16、
JS 15。`TestActiveFiltersFragmentRendering` を含む）で観測可能な挙動が検証されている。
`items_tags.js` の AbortController slot 共有規約も維持されており、design-less impl の機能
境界も Items UI に閉じている。

RESULT: approve
