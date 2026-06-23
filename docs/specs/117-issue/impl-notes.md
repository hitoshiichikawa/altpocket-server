# Implementation Notes — Issue #117 タグにホバー視覚フィードバックとクリック絞り込みを付与する

## 変更ファイル一覧

| ファイル | 種類 | 役割 |
|---|---|---|
| `templates/items_list.html` | 変更 | `<span class="tag">` を `<button class="tag tag-filter-toggle" data-tag-filter-toggle data-tag-normalized="..." aria-pressed="..." aria-label="...">` に置き換え。`$.SelectedTags` を見て初期 SSR 段階で `aria-pressed="true"` / `is-selected` を付与する |
| `templates/items.html` | 変更 | `/static/items_tags.js` を `defer` でロード追加 |
| `static/style.css` | 変更 | `.tag.tag-filter-toggle` の hover / focus-visible / `[aria-pressed="true"]` / `is-selected` / `:disabled` スタイル追加。既存デザイントークン (`--color-primary-soft` / `--color-primary` / `--motion-fast` / `--ease-default` / `--space-2` / `--radius-sm`) のみ使用 (NFR 3.1) |
| `static/items_tags.js` | 新規 | タグボタンのクリック・キーボード活性化を受けて URL `?tag=<normalized>` を toggle、pushState、サイドバー checkbox との双方向同期、フラグメント取得 (`X-Requested-With: ItemsFragment`)、AbortController による前段リクエスト破棄、popstate ハンドリング |
| `static/items_tags.test.mjs` | 新規 | 上記ロジックの単体テスト (node:test + vm)。AC 番号と 1:1 で対応 |
| `static/items_search.js` | 変更 | フラグメント取得 AbortController を `[data-items-region]` 上の `__itemsFragmentInflight` slot 経由で `items_tags.js` と共有（PR #136 round 1 iteration: cross-module race 対策）|
| `static/items_fragment_race.test.mjs` | 新規 | `items_search.js` × `items_tags.js` の cross-module race 回帰テスト。両モジュールを同一 vm context にロードし、片方の新規 fetch が他方の保留 fetch を abort することを検証 |
| `internal/ui/render_test.go` | 変更 | `TestItemsTagsDivRendering` を新テンプレ契約に追従。`TestItemsTagSelectedState` を新規追加し AC 1.4 / 4.3 を担保 |

## 実装判断ログ（Open Questions の解釈）

PM が requirements.md に Open Questions として残した (a)(b)(c) について、本実装は下記の解釈を採用しました。いずれも「既存実装と整合させる」「ユーザの直感を優先する」の 2 軸で選択しています。

### (a) 履歴粒度 → **pushState 相当を採用**

タグクリックは「明示的なコミット操作」（debounced typing と異なり、1 click = 1 確定）であり、`items_search.js` における Enter 押下時の pushState と同質のイベントと見なせます。ユーザが複数タグを連続クリックして絞り込みを徐々に絞ったあと、「戻る」で 1 個前のタグセットに戻れることはユーザの直感に合致するため、pushState を採用しました。

### (b) 複数選択中に未選択タグをクリック → **追加（add）を採用**

サイドバーが checkbox 形式で複数選択を許容している（既存挙動）ことに整合させました。カード上の click で「単独置換」にしてしまうと、ユーザが「サイドバーで go と news を選択 → カード上の rust をクリック」した際に既存選択が消えてしまい、サイドバー操作との振る舞いが分裂してしまいます。追加トグルにすることで「サイドバーから操作してもカードから操作しても同じ最終状態」が保証され、要件 5.3 とも整合します。

### (c) ロード中の連続クリック → **AbortController で前段破棄**

`items_search.js` の `inflight` パターンに揃えました。連続クリックで複数の fetch が同時に飛ぶと、後段の絞り込み結果が前段に上書きされるレース条件が発生するため、最新のクリックの結果だけを必ず表示するように `AbortController` で前段を `.abort()` します。テスト `NFR 1.2 / OQ-(c): 連続クリック時、前段の保留中 fetch が AbortController で破棄される` で担保。

**PR #136 round 1 iteration 追加対応**: 上記 `inflight` は当初 `items_tags.js` 内ローカル変数だったため、検索 debounce 側 (`items_search.js`) の保留 fetch を abort できず、URL とボタン状態はタグ済みなのに一覧だけ古い検索結果に戻る cross-module race が残っていました（要件 2.1 / 5.3 の「絞り込み結果を表示する」違反）。本対応で `[data-items-region]` 要素上の `__itemsFragmentInflight` slot を新設し、`items_tags.js` と `items_search.js` の双方が同じ AbortController を共有するように変更しました。これにより、どちらの起源の新規 fetch も他方の保留 fetch を確実に abort します。`static/items_fragment_race.test.mjs` で双方向の abort と slot 共有を回帰として担保。

## テスト結果

### Go テスト (`go test ./...`)

```
ok  	altpocket/cmd/worker
ok  	altpocket/internal/auth
ok  	altpocket/internal/config
ok  	altpocket/internal/crypto
ok  	altpocket/internal/fetcher
ok  	altpocket/internal/mcpserver
ok  	altpocket/internal/ratelimit
ok  	altpocket/internal/server	4.012s
ok  	altpocket/internal/store
ok  	altpocket/internal/tag
ok  	altpocket/internal/ui
ok  	altpocket/internal/urlnorm
```

### Go vet (`go vet ./...`)

エラーなし（出力なし）。

### gofmt (`gofmt -l .`)

本実装で編集した `internal/ui/render_test.go` は差分なし（既存の `internal/server/server.go` / `internal/server/health_test.go` は本 Issue で触っていないため `gofmt -l` 結果に残るが、これは pre-existing で本 PR 範囲外）。

### JS テスト (`node --test static/items_tags.test.mjs static/items_search.test.mjs`)

```
1..26
# tests 26
# pass 26
# fail 0
```

- `items_tags.test.mjs` 新規 11 件 すべて pass
- `items_search.test.mjs` 既存 15 件 すべて pass（互換確認）

### 拡張機能テスト (`node --test extension/sidepanel.test.mjs`)

```
1..30
# tests 30
# pass 30
# fail 0
```

本 Issue の変更は拡張機能側のコードに触れていないため、想定どおり影響なし。

### lint (`golangci-lint run`)

ローカル環境に `golangci-lint` v2.11.4 が未インストールのため、本実装環境では実行できていません。`go vet` と `gofmt -l` のみで pre-lint チェックを行いました。CI（GitHub Actions）の `golangci-lint-action@v9` が PR 段階で実行されるため、そこで担保されます。

## AC（受入基準）とテストの対応表

| AC | 担保するテスト |
|---|---|
| 1.1 ポインタ形状 | CSS `.tag.tag-filter-toggle { cursor: pointer; }` (style.css) — 手動 E2E |
| 1.2 ホバー視覚フィードバック | CSS `.tag.tag-filter-toggle:hover { background: var(--color-primary-soft); color: var(--color-primary); }` — 手動 E2E |
| 1.3 フォーカス視覚フィードバック | CSS `.tag.tag-filter-toggle:focus-visible { outline: ... }` + 既存共通 `:focus-visible` ルール — 手動 E2E |
| 1.4 選択中の視覚状態 | `TestItemsTagSelectedState`（Go）`要件 1.4 / 4.3: 初期 SSR レンダリング ...`（JS） |
| 2.1 未選択 click → 追加 | `要件 2.1 / 2.4 / 3.1: 未選択タグをクリックすると tag が追加され pushState される` |
| 2.2 選択 click → 除外 | `要件 2.2 / 2.5 / 3.3: 選択中タグをクリックすると除外され ...` |
| 2.3 サイドバー checkbox 連動 | `要件 2.3: タグクリックでサイドバーの同名チェックボックスも追従する` |
| 2.4 URL 更新 | `要件 2.1 / 2.4 / 3.1` および `要件 2.4: pushState の URL が getAll("tag") ...` |
| 2.5 最後の 1 つ解除 → 絞り込みなし | `要件 2.2 / 2.5 / 3.3` および `要件 2.5: 複数選択中で 1 つだけ残った状態から click ...` |
| 3.1 URL クエリ形式 | `要件 2.1 / 2.4 / 3.1`, `要件 2.4: pushState の URL ...` |
| 3.2 他クエリ保持 | `要件 3.2: タグ以外のクエリ (q / sort / per_page / page) は保持される` |
| 3.3 空 tag は URL から除去 | `要件 2.2 / 2.5 / 3.3`, `要件 2.5: 複数選択中で 1 つだけ ...` |
| 3.4 戻る/進む対応 | `要件 3.4: popstate で URL の tag に UI とフラグメントを揃え直す` |
| 4.1 キーボード到達 | `<button type="button">` ネイティブの tab 順序 — 手動 E2E |
| 4.2 キーボード活性化 | `<button>` がブラウザによって Enter/Space を click にディスパッチする仕様に依拠。テスト内でも `click` ハンドラを target=button で呼ぶことで担保 |
| 4.3 押下状態の支援技術公開 | `aria-pressed="true"` 属性 — `TestItemsTagSelectedState`（Go） |
| 4.4 アクセシブル名称 | `aria-label="タグで絞り込み: {{.Name}}"` — テンプレート目視確認 + Go テンプレートテスト |
| 5.1 既存サイドバー絞り込み挙動の維持 | 既存 `items_search.js` および `app.js` の `tagCheckboxes` の挙動は変更していない（追加のみ）。既存テスト `items_search.test.mjs` 15 件すべて pass |
| 5.2 サイドバー変更 → カード反映 | `要件 5.2: サイドバーのチェックボックス変更で同名タグボタンの aria-pressed / is-selected が更新される` |
| 5.3 タグクリックとサイドバー由来の結果一致 | 双方向同期テスト（2.3, 5.2）+ サーバ側は同じ `parseTagFilters` を通るため意味的に同一（既存 `TestParseTagFilters` で担保） |
| NFR 1.1 300ms 以内反応 | `syncControls(tags)` を `refreshFragment` 前に呼ぶ実装。最初のクリックで即座に aria-pressed を更新する経路はテストで担保（fetch 完了を待たない） |
| NFR 1.2 ちらつき防止 | `refreshFragment` で fetch 完了後に `region.innerHTML = html` を一度だけ実行。失敗時は何もしない（既存 `items_search.js` と同じ規約）。テスト `NFR 1.2 / OQ-(c)` で AbortController 経路を担保。cross-module race は `items_fragment_race.test.mjs` の 3 件で担保（タグ→検索 / 検索→タグ の双方向 abort と slot 共有） |
| NFR 2.1 JS 無効でも閲覧と既存フォーム送信が動く | `<button type="button">` は form submit を発火しないため、JS 無効環境ではクリックしても何も起きない（要件 1.2 ホバーは CSS のみで成立 / サイドバー form は従来どおり機能）。手動検証で再確認 |
| NFR 2.2 既存 URL 互換 | `parseTagFilters`（Go 側）が `?tag=` を従来どおり読む。本 PR ではサーバ側の URL 解釈は変更していない。既存 `TestParseTagFilters` で担保 |
| NFR 3.1 デザイントークン統一 | 追加 CSS は既存トークン（`--color-primary` / `--color-primary-soft` / `--color-primary-hover` / `--motion-fast` / `--ease-default` / `--space-2` / `--radius-sm`）のみ使用 — コードレビューで担保 |

## 確認事項（レビュー時の論点・派生）

1. **キーボード活性化のブラウザ互換**: `<button type="button">` は Enter / Space で click イベントを発火するのが HTML 仕様だが、AC 4.2 が「アクティブ化操作」を抽象化しているため、Space と Enter どちらかを限定する/しないかは設計範囲外として実装側で「ブラウザ既定」を採用しました。手動 E2E で双方を確認する想定です。
2. **モバイルのボトムシートサイドバー**: `items.html` には `#filter-sheet` 内にも同じ form / checkbox が存在します。`document.querySelectorAll('input[type="checkbox"][name="tag"]')` で双方をまとめて拾うため、デスクトップサイドバーとモバイルボトムシートのどちらに変更を加えても整合します（テストで明示的に検証はしていないため、手動 E2E で確認推奨）。
3. **拡張機能側との影響**: 拡張機能は `/v1/items` API を使うため UI 変更の影響を受けません（`extension/sidepanel.test.mjs` も pass）。
4. **fragment 再描画時のサイドバー非更新**: 既存実装の意図（`fragmentOnly` の場合は `Tags` facet を取得しない）は維持。タグクリックでフラグメント再描画後、サイドバーの「(N)」カウントは更新されないという既存制約を引き継ぎますが、これは Issue #114 由来の意図された挙動です。
5. **aria-label の日本語**: 既存テンプレートの aria-label は英語混在（"Search articles" 等）ですが、`tag-list` まわりは日本語（既存 `tag-title` など）なので、本実装でも `aria-label="タグで絞り込み: {{.Name}}"` を日本語にしました。ローカライズ統一は本 Issue の Out of Scope なので、UX レビューで「英語に統一する」判断が出れば別 Issue で扱うことを提案します。
6. **`golangci-lint` のローカル不備**: 上記のとおりローカルでは `golangci-lint` を実行できていません。CI が必ず走るため出火点は CI 通過時で検出可能ですが、念のためレビュワーが手元で `golangci-lint run` を流すことを推奨します。

## 派生として切り出せる Issue 候補

- アイテム詳細ページ（`/ui/items/{id}`）でのタグクリック絞り込み（本 Issue は Out of Scope）
- 拡張機能（Chrome Extension）からの一覧表示でのタグ絞り込み UI（同上）
- タグ絞り込みの AND/OR 切り替え UI（同上）

STATUS: complete
