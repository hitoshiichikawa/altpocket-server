# Design Document: web-page-titles

## Overview

**Purpose**: Web UI全ページの `<title>` を `{ページ名} | altpocket` 形式に統一し、ブラウザタブの識別性とアクセシビリティを向上させる。

**Users**: altpocketのWeb UIユーザーが、複数タブ環境・ブラウザ履歴・スクリーンリーダーでページを正確に識別できるようになる。

**Impact**: 既存の6ハンドラーのタイトル文字列と、テンプレート `layout.html` の `<title>` タグを変更する。

### Goals
- 全ページのタイトルを `{ページ名} | altpocket` 形式で統一する
- 記事詳細ページで記事タイトルを動的に反映する
- タイトルなし記事に対するフォールバックを提供する

### Non-Goals
- `<meta>` description やOGPタグの設定（将来課題）
- SPA的なクライアントサイドでのタイトル動的更新
- 英語/多言語対応（現時点では日本語固定）

## Architecture

### Existing Architecture Analysis

現在のタイトル設定は以下の2層で構成されている：

1. **ハンドラー層**（`internal/server/server.go`）: `data["Title"] = "固定文字列"` でページ名をハードコード
2. **テンプレート層**（`templates/layout.html`）: `<title>{{.Title}}</title>` でそのまま出力

この構造は変更せず、以下の2点のみ修正する：
- テンプレートで接尾辞 ` | altpocket` を付与する形式に変更
- 各ハンドラーの `"Title"` 値を日本語の説明的な名前に変更

### Architecture Pattern & Boundary Map

**Architecture Integration**:
- Selected pattern: 既存のテンプレート変数パターンを維持（変更なし）
- Domain/feature boundaries: ハンドラー → テンプレートの単方向データフロー
- Existing patterns preserved: `map[string]interface{}` によるテンプレートデータ受け渡し
- New components rationale: 新規コンポーネントなし
- Steering compliance: SSR + Vanilla JS パターンを維持

### Technology Stack

| Layer | Choice / Version | Role in Feature | Notes |
|-------|------------------|-----------------|-------|
| Backend | Go 1.22 / chi v5 | ハンドラーでのタイトル値設定 | 既存利用 |
| Template | Go `html/template` | タイトル形式の組み立て・XSSエスケープ | 既存利用 |

## Requirements Traceability

| Requirement | Summary | Components | Interfaces | Flows |
|-------------|---------|------------|------------|-------|
| 1.1 | 全ページ `{ページ名} \| altpocket` 形式 | layout.html テンプレート | テンプレート変数 `{{.Title}}` | — |
| 1.2 | サービス名の一貫した接尾辞 | layout.html テンプレート | テンプレート変数 `{{.Title}}` | — |
| 2.1 | ホーム → 「ログイン」 | handleHome | data["Title"] | — |
| 2.2 | 登録 → 「アカウント登録」 | handleRegister | data["Title"] | — |
| 2.3 | 一覧 → 「記事一覧」 | handleUIItems | data["Title"] | — |
| 2.4 | 詳細 → 「{記事タイトル}」 | handleUIItem | data["Title"] + item.Title | — |
| 2.5 | クイック追加 → 「クイック追加」 | renderUIQuickAdd | data["Title"] | — |
| 2.6 | 設定 → 「設定」 | handleUISettings | data["Title"] | — |
| 3.1 | 空タイトルのフォールバック | handleUIItem | data["Title"] 条件分岐 | — |

## Components and Interfaces

| Component | Domain/Layer | Intent | Req Coverage | Key Dependencies | Contracts |
|-----------|--------------|--------|--------------|------------------|-----------|
| layout.html | Template | タイトル形式の統一 | 1.1, 1.2 | 全ハンドラー (P0) | — |
| handleHome | Handler | ログインページタイトル | 2.1 | — | — |
| handleRegister | Handler | 登録ページタイトル | 2.2 | — | — |
| handleUIItems | Handler | 記事一覧タイトル | 2.3 | — | — |
| handleUIItem | Handler | 記事詳細タイトル+フォールバック | 2.4, 3.1 | store.GetItemDetail (P0) | — |
| renderUIQuickAdd | Handler | クイック追加タイトル | 2.5 | — | — |
| handleUISettings | Handler | 設定ページタイトル | 2.6 | — | — |

### Template Layer

#### layout.html

| Field | Detail |
|-------|--------|
| Intent | `<title>` タグで `{ページ名} \| altpocket` 形式を組み立てる |
| Requirements | 1.1, 1.2 |

**変更内容**

現在:
```html
<title>{{.Title}}</title>
```

変更後:
```html
<title>{{.Title}} | altpocket</title>
```

**Implementation Notes**
- テンプレート1箇所の変更で全ページに接尾辞が適用される
- `{{.Title}}` はGo `html/template` により自動エスケープされる

### Handler Layer

#### handleUIItem（記事詳細）

| Field | Detail |
|-------|--------|
| Intent | 記事タイトルを `<title>` に反映し、タイトルなしの場合はフォールバック |
| Requirements | 2.4, 3.1 |

**Responsibilities & Constraints**
- `item.Title` が空でない場合: そのまま `data["Title"]` に設定
- `item.Title` が空文字列の場合: `"(無題)"` を `data["Title"]` に設定
- 判定は `strings.TrimSpace()` ではなく `item.Title == ""` の単純比較（DBのNOT NULL制約によりnilは発生しない）

**Dependencies**
- Inbound: chi router — URLパラメータ `{id}` (P0)
- Outbound: store.GetItemDetail — 記事データ取得 (P0)

**Implementation Notes**
- 既存の `handleUIItem` 内で `"Title": "Item"` を動的値に置換するのみ
- XSSリスクは `html/template` の自動エスケープで緩和済み

#### 静的タイトルハンドラー（handleHome, handleRegister, handleUIItems, renderUIQuickAdd, handleUISettings）

| Field | Detail |
|-------|--------|
| Intent | 各ページに対応する日本語タイトルを設定 |
| Requirements | 2.1, 2.2, 2.3, 2.5, 2.6 |

**タイトルマッピング**

| ハンドラー | 現在の値 | 新しい値 |
|-----------|---------|---------|
| handleHome | `"Sign In"` | `"ログイン"` |
| handleRegister | `"Register"` | `"アカウント登録"` |
| handleUIItems | `"Items"` | `"記事一覧"` |
| renderUIQuickAdd | `"Quick Add"` | `"クイック追加"` |
| handleUISettings | `"Settings"` | `"設定"` |

**Implementation Notes**
- 各ハンドラーの `"Title"` 値を対応する日本語文字列に変更するのみ
- テンプレート側で ` | altpocket` が自動付与されるため、ハンドラーにはページ名部分のみ設定

## Testing Strategy

### Unit Tests（Go テスト）
1. `handleHome` レスポンスの `<title>` が `ログイン | altpocket` を含むことを検証
2. `handleRegister` レスポンスの `<title>` が `アカウント登録 | altpocket` を含むことを検証
3. `handleUIItems` レスポンスの `<title>` が `記事一覧 | altpocket` を含むことを検証
4. `handleUIItem` でタイトルありの記事を表示した場合、`<title>` が `{記事タイトル} | altpocket` を含むことを検証
5. `handleUIItem` でタイトルなしの記事を表示した場合、`<title>` が `(無題) | altpocket` を含むことを検証
6. `renderUIQuickAdd` レスポンスの `<title>` が `クイック追加 | altpocket` を含むことを検証
7. `handleUISettings` レスポンスの `<title>` が `設定 | altpocket` を含むことを検証
