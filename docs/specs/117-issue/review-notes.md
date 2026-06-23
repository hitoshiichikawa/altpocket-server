# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-06-23T06:57:46Z -->

## Reviewed Scope

- Branch: claude/issue-117-impl-issue
- HEAD commit: f208e0761f7aafee3df07526db09bd1a965a3789
- Compared to: develop..HEAD
- Note: 本 Issue は design-less impl 経路（`docs/specs/117-issue/` に
  `design.md` / `tasks.md` は存在しない）。3 カテゴリ判定は requirements.md と
  diff の突き合わせで実施した。CLAUDE.md の `## Feature Flag Protocol` 採否は
  `opt-out` のため flag 観点の細目チェックは対象外。

## Verified Requirements

- 1.1 ポインタ形状 — `static/style.css` の `.tag.tag-filter-toggle { cursor: pointer; }`
- 1.2 ホバー視覚フィードバック — `static/style.css` の `.tag.tag-filter-toggle:hover { background: var(--color-primary-soft); color: var(--color-primary); }`
- 1.3 フォーカス視覚フィードバック — `static/style.css` の `.tag.tag-filter-toggle:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }`
- 1.4 選択中の視覚状態 — `internal/ui/render_test.go::TestItemsTagSelectedState` (3 サブテスト) + JS テスト `要件 1.4 / 4.3: 初期 SSR レンダリング ...`
- 2.1 未選択 click → 追加 — `static/items_tags.test.mjs::要件 2.1 / 2.4 / 3.1`
- 2.2 選択 click → 除外 — `static/items_tags.test.mjs::要件 2.2 / 2.5 / 3.3`
- 2.3 サイドバー checkbox 連動 — `static/items_tags.test.mjs::要件 2.3`
- 2.4 URL 更新 — `static/items_tags.test.mjs::要件 2.4: pushState の URL ...`
- 2.5 最後の 1 つ解除 → 絞り込みなし — `static/items_tags.test.mjs::要件 2.5`
- 3.1 URL クエリ形式 (`?tag=<normalized>` 複数指定可) — `static/items_tags.js::buildToggledURL` + 同名 JS テスト
- 3.2 他クエリ保持 — `static/items_tags.test.mjs::要件 3.2: タグ以外のクエリ ...`
- 3.3 空 tag は URL から除去 — `static/items_tags.js::buildToggledURL` (set 空→`searchParams.delete('tag')`) + `要件 2.5` テスト
- 3.4 戻る/進む対応 — `static/items_tags.test.mjs::要件 3.4: popstate ...`
- 4.1 キーボード到達 — `<button type="button">` ネイティブ tab 順序 (`templates/items_list.html` で button 化) + `internal/ui/render_test.go` で `type="button"` の出力を assert
- 4.2 キーボード活性化 — `<button>` の Enter/Space → click 仕様に依拠（JS テストは target=button の click ハンドラ呼び出しで等価経路を駆動）
- 4.3 押下状態の支援技術公開 — `aria-pressed="true|false"` 出力 (`internal/ui/render_test.go::TestItemsTagSelectedState` の aria-pressed count assertion)
- 4.4 アクセシブル名称 — `templates/items_list.html` の `aria-label="タグで絞り込み: {{.Name}}"`
- 5.1 既存サイドバー挙動の維持 — `items_search.js` / `app.js` の checkbox 経路は差分なし。回帰として `static/items_search.test.mjs` 15 件すべて pass（impl-notes 実行ログで確認）
- 5.2 サイドバー → カード反映 — `static/items_tags.test.mjs::要件 5.2`
- 5.3 タグクリックとサイドバー由来の結果一致 — サーバ側 `parseTagFilters` (`internal/server/server.go:1438`) と `selectedTagSet` (`:1447`) は無変更。同一 URL クエリ形式に揃えるため絞り込み条件が等しければ結果も一致
- NFR 1.1 300ms 以内反応 — `commitToggle` が fetch 完了を待たず `syncControls(tags)` を即時呼び出す（JS テスト `要件 2.1 / 2.4 / 3.1` でボタン UI 即時更新を assert）
- NFR 1.2 ちらつき防止 — `refreshFragment` で fetch 成功時のみ `region.innerHTML = html` を一度実行。失敗時は前回結果維持。`NFR 1.2 / OQ-(c): 連続クリック時、前段の保留中 fetch が AbortController で破棄される` でレース対策を担保
- NFR 2.1 JS 無効環境互換 — タグは `<button type="button">` で form を暗黙 submit しない。サイドバー form は無変更 (`grep` 確認済み)
- NFR 2.2 既存 URL 互換 — サーバ側 `parseTagFilters` は本 PR で無変更（既存 `TestParseTagFilters` で担保）
- NFR 3.1 デザイントークン統一 — `static/style.css` 追加分は `--color-primary`, `--color-primary-soft`, `--color-primary-hover`, `--motion-fast`, `--ease-default`, `--space-2` のみ使用（diff 目視確認）

## Findings

なし

## Summary

requirements.md の全 numeric ID（1.1〜1.4 / 2.1〜2.5 / 3.1〜3.4 / 4.1〜4.4 / 5.1〜5.3 /
NFR 1.1〜1.2 / NFR 2.1〜2.2 / NFR 3.1）について、実装または対応テストが diff 内に
確認できた。`SelectedTags` ハンドラ側で `selectedTagSet(tagFilters)` を fragment / 全画面
双方の `data` に渡すパスも検証済み。Go テスト・JS テスト・拡張機能テストが impl-notes
記載の通り green（合計 56 件 pass）で、design-less impl の境界（Items UI に限定）にも
逸脱なし。

RESULT: approve
