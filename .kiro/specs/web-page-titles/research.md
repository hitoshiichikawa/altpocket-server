# Research & Design Decisions

## Summary
- **Feature**: `web-page-titles`
- **Discovery Scope**: Simple Addition（既存パターンの拡張）
- **Key Findings**:
  - タイトル設定は各ハンドラーの `map[string]interface{}` 内 `"Title"` キーのハードコード文字列で完結しており、テンプレート側は `{{.Title}}` で単純出力する構造
  - 記事詳細ページのみ動的タイトル（`item.Title` からの派生）が必要で、フォールバック処理が発生する
  - 新規ライブラリ・DB変更・テンプレート構造変更は不要

## Research Log

### 既存のタイトル設定パターン
- **Context**: 各ページのタイトルがどのように設定されているか
- **Sources Consulted**: `internal/server/server.go`、`templates/layout.html`
- **Findings**:
  - 全6ページが `data := map[string]interface{}{"Title": "固定文字列", ...}` でタイトルをハードコード
  - テンプレート `layout.html` 行9: `<title>{{.Title}}</title>` でそのまま出力
  - サービス名の接尾辞は付与されていない
- **Implications**: 各ハンドラーの `"Title"` 値を書き換えるだけで対応可能。テンプレートの変更は不要

### 記事詳細ページの動的タイトル
- **Context**: 記事タイトルをページタイトルに反映する際のデータアクセス
- **Sources Consulted**: `internal/store/store.go`（`ItemDetail` 構造体）、`handleUIItem`
- **Findings**:
  - `ItemDetail` は `Item` を埋め込み、`Item.Title` フィールド（`string`型）を持つ
  - `handleUIItem` は `s.store.GetItemDetail()` で記事を取得後に `data` マップを構築
  - `item.Title` が空文字列の場合のフォールバックが必要
- **Implications**: ハンドラー内で `item.Title` を判定し、空なら `"(無題)"` をセットする簡易ロジックで十分

## Design Decisions

### Decision: タイトル構築の責務配置
- **Context**: `{ページ名} | altpocket` 形式のタイトルをどこで組み立てるか
- **Alternatives Considered**:
  1. テンプレート側で接尾辞を付与（`<title>{{.Title}} | altpocket</title>`）
  2. ハンドラー側で完成形のタイトル文字列を渡す
- **Selected Approach**: テンプレート側で接尾辞を付与（Option 1）
- **Rationale**:
  - サービス名の一貫性をテンプレート1箇所で保証できる
  - ハンドラーはページ固有部分のみを責務とし、変更箇所を局所化
  - 将来サービス名が変更された場合もテンプレート1箇所の修正で済む
- **Trade-offs**: ハンドラーが渡す `Title` とブラウザ表示が完全一致しなくなるが、テンプレートの構造が自明なため混乱リスクは低い

## Risks & Mitigations
- 記事タイトルにHTMLエンティティやXSS文字が含まれる場合 → Go `html/template` が自動エスケープするため追加対策不要
- タイトルが極端に長い記事 → ブラウザがタブ表示を自動トランケートするため、アプリ側での文字数制限は不要

## References
- Go `html/template` パッケージ: 自動コンテキストエスケープによるXSS防止
