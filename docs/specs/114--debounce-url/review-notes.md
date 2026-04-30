# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-04-30T00:00:00Z -->

## Reviewed Scope

- Branch: claude/issue-114-impl--debounce-url
- HEAD commit: 879f299f31779ca7463a4001b7834de375424ba4
- Compared to: main..HEAD
- Feature Flag Protocol: opt-out（CLAUDE.md L303）— flag 観点の細目チェックは適用しない

確認した変更ファイル（10 ファイル）:

- `internal/server/server.go`（handleUIItems に fragment 経路 + `wantsItemsFragment` 追加）
- `internal/server/server_test.go`（`TestWantsItemsFragment` table-driven）
- `internal/ui/render.go`（`RenderFragment` 追加 / `fragments` map）
- `internal/ui/render_test.go`（`TestRenderFragmentItemsList` 3 サブテスト + `TestItemsPageEmbedsFragment`）
- `static/items_search.js`（新規 / 253 行 / debounce + URL sync + popstate + IME）
- `static/items_search.test.mjs`（新規 / 506 行 / 14 ケース）
- `templates/items.html`（items_list partial 呼び出しへ置換 + `data-items-region` + script include）
- `templates/items_list.html`（新規 partial）
- `docs/specs/114--debounce-url/{requirements,impl-notes}.md`（spec dir 追加）

`tasks.md` / `design.md` は本 Issue では生成されていない（Architect 不在のシンプル Issue 想定）。
boundary 制約は `requirements.md` の Out of Scope と `CLAUDE.md` の禁止事項で代替判定する。

## Verified Requirements

### Requirement 1: 入力停止後の自動絞り込み

- 1.1 — `static/items_search.test.mjs` `R1 AC-1: input then 300ms idle triggers a single fragment fetch with q`（`fetchCalls.length === 1`、`q=rust` 検証）
- 1.2 — `R1 AC-2 / NFR 1.2: rapid edits within debounce window collapse to one fetch with last value`（`timer.pending() === 1` で先行タイマ破棄を検証、最終値で 1 回 fetch）
- 1.3 — `R1 AC-3: focused (active) input is not overwritten by syncInputs (caret preservation)` および `items_search.js` L82-L86 の `if (el === active) continue;` ロジック
- 1.4 — `R1 AC-4 / NFR 2.1: fragment fetch uses the same /ui/items path with all existing query params`（pathname / sort / per_page / tag を検証）
- 1.5 — `static/app.js` の既存 select / tag-checkbox 自動 submit ハンドラに変更が無いこと、および `items_search.js` が `input[name="q"]` のみを対象にしていること（L37 の `querySelectorAll('input[name="q"]')` 限定）でコードレビュー上カバー

### Requirement 2: URL クエリの同期

- 2.1 — `R2 AC-1: debounce-driven sync uses history.replaceState (not pushState)`（`replaces.length === 1`、`pushes.length === 0` を検証）
- 2.2 — `R2 AC-2 / R5 AC-3: clearing the input drops q from the URL`（`searchParams.get('q') === null`、ただし `sort=relevance` は保持を併せて検証）
- 2.3 — `R2 AC-3: other query params (sort / per_page / tag / page) are preserved on q sync`（`page=3` を含む全パラメータ保持を検証）
- 2.4 — サーバ側 `internal/server/server.go` L740 `q := r.URL.Query().Get("q")` および L775 `"Query": q` は無変更で、`templates/items.html` の `value="{{.Query}}"` も維持。fragment 経路はヘッダで分岐するだけで URL クエリ仕様を変えていないことを `TestWantsItemsFragment`（NFR 2.1）が間接保証

### Requirement 3: ブラウザ履歴ナビゲーション

- 3.1 — `R3 AC-1 / AC-2: popstate refreshes input value and refetches fragment from new URL`（`region.innerHTML === '<x>after-popstate</x>'`）
- 3.2 — 同テスト内で `inputs[0].value === 'back'` を検証
- 3.3 — OQ-1 採用方針として impl-notes に記録された通り、debounce 時 replaceState（R2 AC-1 テストが検証）/ Enter 時 pushState（R4 AC-1 テストが検証）のハイブリッドを実装

### Requirement 4: Enter キーによる即時反映

- 4.1 — `R4 AC-1 / AC-2: Enter triggers immediate fetch, cancels pending debounce, uses pushState`（`timer.pending() === 0` を Enter 後に検証）
- 4.2 — 同テストで「孤児タイマを `runAll` しても `fetchCalls.length === 1` のまま」を検証（debounce 由来の二重 fetch なし）
- 4.3 — 同テストで `pushes[0].url` の `q=rust` を検証（R2 と同規則で同期）

### Requirement 5: 空入力で未絞り込みに戻す

- 5.1 — `R2 AC-2 / R5 AC-3` テストで空入力時 fetch URL から q が落ちていること（`url.searchParams.get('q') === null`）を検証
- 5.2 — `R5 AC-2: whitespace-only input is treated as empty (q removed)`（`'   '` で `q` が URL から消える）
- 5.3 — `R2 AC-2 / R5 AC-3` テストで replaceState の URL から q 削除を検証

### Requirement 6: 既存フィルタ UI との整合

- 6.1 — `R6 AC-1: typing in one search input syncs the value to the other inputs`（`inputs[1].value === 'rust'`、`inputs[2].value === 'rust'` を検証）
- 6.2 — `R6 AC-2: same idempotent input does not re-fetch after debounce (no spurious double-submit)`（URL の `q=rust` と同じ値の入力で `fetchCalls.length === 0`）。既存 Apply / tag-checkbox 経路は `static/app.js` 無変更でコードレビュー確認
- 6.3 — `templates/items.html` の form L9 `<form class="search-bar" ... method="get" action="/ui/items">`、L24 `<form id="filter-form" method="get" action="/ui/items">`、L79 `<form method="get" action="/ui/items">` の 3 つすべて維持。`items_search.js` は `input/keydown/composition` リスナを追加し、`keydown` Enter は `e.preventDefault()` のみで form 自体は壊さない。JS 無効環境ではスクリプトが評価されず form submit が従来通り動作する

### Non-Functional Requirements

- NFR 1.1 — `R1 AC-2 / NFR 1.2` テストで「300ms 以内連続入力中は新規 fetch なし」を `fetchCalls.length === 0`（タイマー未発火時点）+ `pending() === 1` で検証。`DEBOUNCE_MS = 300` 定数（L23）
- NFR 1.2 — 同テスト + `items_search.js` L108-L112 の `inflight.abort()` で保留中 fetch を最新で置き換え
- NFR 2.1 — `TestWantsItemsFragment`（境界値 7 ケース + nil request）で「ヘッダ無し / 他値 / 空 / 大文字小文字 / 前後空白」すべて分岐を検証。サーバ側 URL クエリ仕様（`q` / `sort` / `per_page` / `tag` / page）は L740-L744 で無変更
- NFR 2.2 — `go test ./internal/server/ ./internal/ui/` は `ok`（cached）で確認。impl-notes でも `go test ./...` 全体 green を Developer が報告済み
- NFR 3.1 — `templates/items.html` L13 / L27 / 79 行目以降の input に `aria-label="Search articles"` が維持されていること、および `R1 AC-3` テストでフォーカス保持を検証（active 入力欄を `syncInputs` が触らない）
- NFR 3.2 — `NFR 3.2: failed fetch leaves previous innerHTML intact (no flicker)`（500 応答で `region.innerHTML` が前回値のまま）+ `items_search.js` L120-L130 の「レスポンス到着後にのみ `region.innerHTML` を差し替え」設計

### サーバ側 fragment 経路の追加保証

- `TestRenderFragmentItemsList/fragment_contains_items_but_no_layout_chrome`（`<!DOCTYPE>` / `<html>` / `<title>` を含まないことを検証）
- `TestRenderFragmentItemsList/empty_Items_renders_empty-state_card_without_layout`
- `TestRenderFragmentItemsList/unknown_fragment_name_returns_500`
- `TestItemsPageEmbedsFragment`（フルページ render でも items_list partial が含まれ、`#items-list` に `data-items-region` 属性が付与されていること）

## Boundary 確認

- 変更ファイルはすべて SSR / static / spec dir に閉じており、`migrations/` / `extension/` / `cmd/worker` / `internal/store` / `internal/auth` / `internal/mcpserver` / `internal/fetcher` / `internal/urlnorm` / `internal/tag` / `internal/ratelimit` には差分なし
- `requirements.md` Out of Scope（DB スキーマ変更 / 検索 API 新規エンドポイント / モバイル UI レイアウト変更 / 検索アルゴリズム変更）に該当する変更は無し
- `extension_contract_test.go` は無変更で、Chrome 拡張側との後方互換は壊していない
- 環境変数 / マイグレーション / 依存ライブラリの追加なし（impl-notes と一致）
- テスト配置: `*_test.go` が対象パッケージ同一ディレクトリ（`internal/server/server_test.go` / `internal/ui/render_test.go`）、新規 `static/items_search.test.mjs` も対象 JS と同一ディレクトリ。CLAUDE.md「テストは対象コードの近傍に配置」と整合

## Findings

なし（approve）。

## レビュー所見（reject 事由ではない参考情報）

以下は AC を満たしているため reject 対象ではないが、impl-notes でも Developer が
人間判断を仰いでいるため再掲する:

1. **AC 2.3 の `page` 保持**: Developer は requirements の「他のクエリパラメータを保持」を
   字義通り解釈して `page` も保持した実装にしている。一方既存 Apply 送信パスは form に
   `page` hidden field が無いため `page` をドロップする（page=1 リセット）。要件文言には
   合致しているが、UX 上は debounce 経路でも page=1 にリセットする方が直感的という
   見方もできる。要件改定が必要なら次 Issue で扱うのが妥当。**本 review では
   要件文言通り = 合致と判定**
2. **OQ-3 ローディング表示**: Developer は不要と判断。AC / Out of Scope いずれにも
   要求が無いため reject 対象外。実機操作で許容できなければ別 Issue 化を推奨
3. **`golangci-lint run` / `node --test` のローカル未実行**: 当該開発スロットに
   バイナリが無いため Developer が CI 任せにしている。`go test ./...` は
   reviewer 側でも確認済（`go test ./internal/server/ ./internal/ui/` が cached pass）。
   CI（`.github/workflows/ci.yml`）で v2.11.4 + node が走る前提で、現時点の
   reject 事由には該当しない

## Summary

6 つの Requirement と 3 つの NFR、計 21 個の numeric AC すべてが、`static/items_search.test.mjs`
（14 ケース）と `internal/server/server_test.go` の `TestWantsItemsFragment`、`internal/ui/render_test.go`
の `TestRenderFragmentItemsList` / `TestItemsPageEmbedsFragment`、および既存 SSR ハンドラ
（無変更箇所）で実装またはテストカバーされている。boundary 逸脱・missing test・AC 未カバーの
いずれも検出されなかった。

RESULT: approve
