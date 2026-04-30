# Impl Notes — Issue #114 検索 debounce 即時反映 + URL 同期

## 採用方針（Open Questions の取扱い）

PM が `requirements.md` に残した Open Questions について、Developer の合理的判断
として以下方針で実装した。AC は追加・改変していない。

### OQ-1（履歴粒度）

**採用:** debounce 経過時 `history.replaceState`、Enter キー押下時
`history.pushState`。AC R3 AC-3 が「OQ-1 の判断結果に従う」と明記しているため、
Issue 仮案の方針をそのまま採用した。

**根拠:**
- 入力 1 文字ごとに `pushState` を積むと、ブラウザの戻る連打が必要になり履歴が
  汚染される（実装コストではなく利用者の使い勝手の問題）
- 「確定操作」（Enter 押下）を 1 履歴単位とすることで、利用者の認知単位（検索
  クエリ単位）と履歴単位が一致する
- `replaceState` で URL は常に最新状態を保ちつつ、戻る操作で意味のある粒度に
  辿れる

**最終確認:** 人間レビュー時に履歴粒度の妥当性を確認してください。

### OQ-2（結果一覧の更新方式）

**採用:** 既存ハンドラ `/ui/items` を再利用し、`X-Requested-With: ItemsFragment`
ヘッダで fragment 出力に切り替える方式（OQ-2 案 (a) に相当）。

**根拠:**
- NFR 3.2「ちらつきを起こさない」を満たすため、フルリロード方式 (b) は不採用
- 案 (c)（既存 JSON API `/v1/items`）は、サイドパネル拡張機能用の Bearer 認証経路
  であり、Web SSR の Cookie セッションで使えない上、HTML を返さないので別途
  クライアント側でカード描画ロジックの重複が必要になる。SSR で持っている
  「カードの見た目とテンプレ」の真実点を二重化したくないため不採用
- 既存ハンドラに最小の追加（ヘッダで分岐 + 部分テンプレート切り出し）で済むため、
  NFR 2.1「URL クエリ仕様を変更しない」の制約と整合する

**実装の詳細:**
- `templates/items_list.html`: `<section id="items-list">` 内のカード + ページ
  ネーション部分だけを `{{define "items_list"}}` で切り出した部分テンプレート
- `templates/items.html`: 該当領域を `{{template "items_list" .}}` 呼び出しに
  置換し、`<section id="items-list">` に `data-items-region` 属性を付与
- `internal/ui/render.go`: 既存 `Render` 経路は layout 経由で従来通り、新規
  `RenderFragment` 経路は `items_list` 単体を layout なしで吐き出す
- `internal/server/server.go`: `handleUIItems` でヘッダ判定し、fragment 経路では
  サイドバー用 Tags facet クエリ (`ListTagsWithCountFiltered`) をスキップ
  （結果一覧領域に関係しない）

### OQ-3（ローディング表示）

**採用:** 何も追加しない（ローディングインジケータなし）。

**根拠:**
- 要件 (Req / NFR) に明示の AC が無く、Out of Scope にも入っていない
- NFR 3.2 で「debounce 待機中は前回結果の表示を維持する」が定義されており、
  ちらつき防止のためにレスポンス到着まで前回結果を残す実装になっている
- スピナー表示などを足すと、わずかな入力遅延でも UI がチカチカするため、デザイン
  的にも逆効果になりうる
- 必要であれば次 Issue として扱うべき UX 改善

### OQ-4（IME 中間入力の扱い）

**採用:** `compositionstart` で debounce を抑止、`compositionend` で再開。
変換中の Enter は確定として扱わない（commit を発火しない）。

**根拠:**
- 日本語ユーザーが「日本語」と入力する間、変換候補表示中の中間文字列で
  fetch が走ると検索結果がチラつき、変換確定後の文字列で結局再検索される
  ので無駄
- IME 確定の Enter（変換候補の選択 Enter）と Search 確定の Enter は同じ keydown
  だが、`compositionend` 前 / 後で識別できる。本実装では composing=true 中の
  Enter は `preventDefault` のみ行い `commitImmediate` は呼ばない

## 受入基準のテストカバレッジ（traceability）

すべての numeric AC に対応するテストを以下に列挙する。

### Requirement 1: 入力停止後の自動絞り込み

| AC | テスト | 場所 |
|---|---|---|
| 1.1 | `R1 AC-1: input then 300ms idle triggers a single fragment fetch with q` | `static/items_search.test.mjs` |
| 1.2 | `R1 AC-2 / NFR 1.2: rapid edits within debounce window collapse to one fetch with last value` | `static/items_search.test.mjs` |
| 1.3 | `R1 AC-3: focused (active) input is not overwritten by syncInputs (caret preservation)` | `static/items_search.test.mjs` |
| 1.4 | `R1 AC-4 / NFR 2.1: fragment fetch uses the same /ui/items path with all existing query params` | `static/items_search.test.mjs` |
| 1.5 | （挙動確認）他の `sort` / `per_page` / `tag` change handler は本実装で触っておらず、既存挙動 (`static/app.js` の `form.querySelectorAll('select')` 自動 submit / `form.querySelectorAll('input[type="checkbox"][name="tag"]')` 自動 submit) を変更していないことをコードレビューで確認可能 |

### Requirement 2: URL クエリの同期

| AC | テスト | 場所 |
|---|---|---|
| 2.1 | `R2 AC-1: debounce-driven sync uses history.replaceState (not pushState)` | `static/items_search.test.mjs` |
| 2.2 | `R2 AC-2 / R5 AC-3: clearing the input drops q from the URL` | `static/items_search.test.mjs` |
| 2.3 | `R2 AC-3: other query params (sort / per_page / tag / page) are preserved on q sync` | `static/items_search.test.mjs` |
| 2.4 | サーバ側 `handleUIItems` は変更前から URL の `q` パラメータを読んで `value="{{.Query}}"` に流し込み済み。fragment 経路で URL クエリ仕様を変えていないことを `TestWantsItemsFragment` および既存 `TestPageTitleFormat`（`items shows article list title`）で間接的に保証 |

### Requirement 3: ブラウザ履歴ナビゲーション

| AC | テスト | 場所 |
|---|---|---|
| 3.1 | `R3 AC-1 / AC-2: popstate refreshes input value and refetches fragment from new URL` | `static/items_search.test.mjs` |
| 3.2 | `R3 AC-1 / AC-2: popstate refreshes input value and refetches fragment from new URL`（同テスト内で `inputs[0].value === 'back'` を assert） | `static/items_search.test.mjs` |
| 3.3 | OQ-1 採用方針として impl-notes に記録（Developer 判断）。実装としては `R2 AC-1` （replaceState）と `R4 AC-1`（pushState）でそれぞれ検証 | 本ファイル + `static/items_search.test.mjs` |

### Requirement 4: Enter キーによる即時反映

| AC | テスト | 場所 |
|---|---|---|
| 4.1 | `R4 AC-1 / AC-2: Enter triggers immediate fetch, cancels pending debounce, uses pushState` | `static/items_search.test.mjs` |
| 4.2 | 同上テストで `env.fetchCalls.length === 1` を assert（debounce 由来の二重 fetch が無いこと） | `static/items_search.test.mjs` |
| 4.3 | 同上テストで pushState の URL に `q` が含まれていることを assert（R2 と同じ規則で同期） | `static/items_search.test.mjs` |

### Requirement 5: 空入力で未絞り込みに戻す

| AC | テスト | 場所 |
|---|---|---|
| 5.1 | `R2 AC-2 / R5 AC-3: clearing the input drops q from the URL` | `static/items_search.test.mjs` |
| 5.2 | `R5 AC-2: whitespace-only input is treated as empty (q removed)` | `static/items_search.test.mjs` |
| 5.3 | `R2 AC-2 / R5 AC-3: clearing the input drops q from the URL`（URL から `q` 削除を assert） | `static/items_search.test.mjs` |

### Requirement 6: 既存フィルタ UI との整合

| AC | テスト | 場所 |
|---|---|---|
| 6.1 | `R6 AC-1: typing in one search input syncs the value to the other inputs` | `static/items_search.test.mjs` |
| 6.2 | `R6 AC-2: same idempotent input does not re-fetch after debounce (no spurious double-submit)` および既存 `static/app.js` の `Apply` ボタン送信パスとタグチェックボックス自動送信パスは無変更（コードレビューで確認可能）| `static/items_search.test.mjs` |
| 6.3 | items.html の form 要素は維持され、`items_search.js` は input/keydown ハンドラを追加するだけで form の `action="/ui/items"` `method="get"` は壊さない。JS 無効時はスクリプトが評価されず、Enter で従来通り form submit が走る（コードレビュー確認） |

### Non-Functional Requirements

| AC | テスト | 場所 |
|---|---|---|
| NFR 1.1 | `R1 AC-2 / NFR 1.2: rapid edits within debounce window collapse to one fetch with last value`（300ms 以内連続入力では新規リクエストを送らないことを assert） | `static/items_search.test.mjs` |
| NFR 1.2 | 同上（最後の入力値で 1 回だけ実行されること） | `static/items_search.test.mjs` |
| NFR 2.1 | `TestWantsItemsFragment`（既存 URL クエリ仕様を変えずヘッダだけで分岐）+ `R1 AC-4 / NFR 2.1` | `internal/server/server_test.go`, `static/items_search.test.mjs` |
| NFR 2.2 | `go test ./...` 全体パス（`internal/server/extension_contract_test.go` 含む既存テスト全件 green） | `go test ./...` |
| NFR 3.1 | `R1 AC-3: focused (active) input is not overwritten by syncInputs (caret preservation)`（フォーカス維持・aria-label は HTML 側で維持） | `static/items_search.test.mjs` + items.html の input[name="q"] に `aria-label="Search articles"` 維持 |
| NFR 3.2 | `NFR 3.2: failed fetch leaves previous innerHTML intact (no flicker)`（fetch 失敗時に前回結果を残す）+ レスポンス到着まで region.innerHTML は触らない実装方針 | `static/items_search.test.mjs` |

### サーバ側 fragment 経路の追加テスト（R / NFR と直接 1:1 ではないが実装の正しさを担保）

| テスト | 場所 |
|---|---|
| `TestRenderFragmentItemsList/fragment_contains_items_but_no_layout_chrome` | `internal/ui/render_test.go` |
| `TestRenderFragmentItemsList/empty_Items_renders_empty-state_card_without_layout` | `internal/ui/render_test.go` |
| `TestRenderFragmentItemsList/unknown_fragment_name_returns_500` | `internal/ui/render_test.go` |
| `TestItemsPageEmbedsFragment` （フルページ render でも items_list partial が含まれること） | `internal/ui/render_test.go` |
| `TestWantsItemsFragment` (table-driven、no-header / 完全一致 / 大文字小文字 / spaces / nil request の境界値網羅) | `internal/server/server_test.go` |

## 実行コマンドと結果

```bash
$ go test ./...
ok      altpocket/cmd/worker
ok      altpocket/internal/auth
ok      altpocket/internal/config
ok      altpocket/internal/fetcher
ok      altpocket/internal/mcpserver
ok      altpocket/internal/ratelimit
ok      altpocket/internal/server
ok      altpocket/internal/store
ok      altpocket/internal/tag
ok      altpocket/internal/ui
ok      altpocket/internal/urlnorm

$ go vet ./...
（出力なし、エラーなし）

$ go build ./cmd/api ./cmd/worker
（成功）
```

### 環境制約による未実行コマンド

- `golangci-lint run`: 当該開発スロットには `golangci-lint` バイナリが
  インストールされていなかったため、ローカル実行は未確認。CI（GitHub
  Actions `.github/workflows/ci.yml`）で v2.11.4 が走る前提
- `node --test static/items_search.test.mjs`: 当該開発スロットには Node.js
  がインストールされていなかったため、ローカル実行は未確認。テストファイル
  は既存 `extension/sidepanel.test.mjs` と同じ runtime / API（`node:test` /
  `node:vm` / `node:fs` / `node:assert`）のみ使っており、追加の npm 依存は
  なし。**人間レビュー時に Node がある環境で `node --test
  static/items_search.test.mjs` を回して green を確認してください**

## 確認事項（人間レビュー / Architect への差し戻し候補）

1. **OQ-1 履歴粒度**: 上記 採用方針 OQ-1 の通り `replaceState` / `pushState`
   ハイブリッド方式を採用しました。意図に沿っているか確認してください。
2. **OQ-3 ローディング表示**: 不要と判断して何も追加していません。実機で操作
   してみて、debounce 待機中の体感が許容範囲か確認してください。許容できなければ
   別 Issue 化を提案します。
3. **AC 2.3 の `page` 保持**: 「他のクエリパラメータを保持する」を字義通りに
   解釈して `page` も保持しています。ただし既存の Apply 送信パスは form に
   page hidden field が無いため `page` をドロップする挙動なので、debounce 経路
   と Apply 経路で動きが異なります。UX 上は debounce で `page` をドロップして
   page=1 に戻す方が直感的かもしれません。要件解釈の確認をお願いします。
4. **fragment ヘッダ命名**: `X-Requested-With: ItemsFragment` を採用しました。
   既存の `X-Requested-With: XMLHttpRequest` と衝突しない値であり、
   `wantsItemsFragment` テストで境界値（XMLHttpRequest 値・空・他値）を網羅
   しています。
5. **テスト実行**: `node --test static/items_search.test.mjs` および
   `golangci-lint run` をローカルで未実行（環境にバイナリ無し）。CI / 人間
   レビュー側で確認をお願いします。

## 実装上のメモ

- 新規依存ライブラリは追加していません（標準 `node:test` / `node:vm` /
  `html/template` のみ）
- マイグレーションは追加していません（DB スキーマ変更なし）
- 環境変数の追加・変更はありません（README / .env.example の更新は不要）
- `extension/` 配下は無変更です（`extension_contract_test.go` は既存通り
  green）。Chrome 拡張機能の検索 debounce はもともと `setTimeout` 180ms で
  実装済みなので Web SSR 側だけを今回の対象としました
