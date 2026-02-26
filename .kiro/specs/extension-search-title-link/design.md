# Design Document: extension-search-title-link

## Overview

**Purpose**: Chrome拡張機能の記事一覧・検索結果のタイトル文字列をクリック可能なリンクに変更し、Web UIの記事詳細画面（`/ui/items/{id}`）へ直接遷移できる導線を提供する。

**Users**: altpocketの全ユーザーが、拡張機能のサイドパネルからWeb UIの記事詳細画面への素早いアクセスに利用する。

**Impact**: `extension/sidepanel.js` の `renderItems()` テンプレートと `extension/sidepanel.css` のスタイルを変更する。サーバー側の変更は不要。

### Goals
- タイトルクリックでWeb UI記事詳細を新しいタブで開けるようにする
- 既存のUIデザイン・動作との一貫性を維持する
- XSSエスケープを含むセキュリティの維持

### Non-Goals
- サーバー側APIの変更（`item.id` は既にレスポンスに含まれる）
- 拡張機能内での記事詳細表示（Web UIへの遷移のみ）
- 「Show original」リンクの動作変更

## Architecture

### Existing Architecture Analysis

変更対象は拡張機能のフロントエンド層のみ。

- **現行パターン**: `renderItems()` が `innerHTML` テンプレートリテラルで記事カードを生成。タイトルは `<h3>` プレーンテキスト
- **維持すべき境界**: `escapeHTML()` によるXSSエスケープ、`Show original` リンクの独立性
- **既存の統合ポイント**: `getConfiguredAPIBase()` によるWeb UI URL構築パターン（`openWebUI()` で確立済み）

### Architecture Pattern & Boundary Map

単一コンポーネント内のテンプレート変更であり、アーキテクチャ境界の変更は発生しない。

**Architecture Integration**:
- Selected pattern: 既存テンプレート拡張（テンプレート内の `<h3>` を `<h3><a>` に変更）
- Domain/feature boundaries: Extension Sidepanel UI層のみ
- Existing patterns preserved: `escapeHTML()` によるエスケープ、`target="_blank"` + `rel="noopener noreferrer"` のリンクパターン
- New components rationale: 新規コンポーネントなし
- Steering compliance: 拡張機能のVanilla JS + CSS構成を維持

### Technology Stack

| Layer | Choice / Version | Role in Feature | Notes |
|-------|------------------|-----------------|-------|
| Frontend | Vanilla JS (ES2020+) | テンプレート変更 | 既存の `sidepanel.js` を修正 |
| Frontend | CSS3 | リンクスタイル追加 | 既存の `sidepanel.css` に追記 |

## Requirements Traceability

| Requirement | Summary | Components | Interfaces | Flows |
|-------------|---------|------------|------------|-------|
| 1.1 | タイトルをハイパーリンクとしてレンダリング | renderItems | — | — |
| 1.2 | href属性を `{webBaseURL}/ui/items/{item.id}` で生成 | renderItems, getConfiguredAPIBase | — | — |
| 1.3 | 新しいタブでWeb UI詳細画面を開く | renderItems | — | — |
| 1.4 | タイトル空文字時の "(untitled)" フォールバック | renderItems | — | — |
| 2.1 | API接続先からWeb UIベースURLを導出 | getConfiguredAPIBase | — | — |
| 2.2 | `/ui/items/{id}` パスを結合して完全URL構築 | renderItems | — | — |
| 3.1 | タイトルリンクにXSSエスケープ適用 | renderItems, escapeHTML | — | — |
| 3.2 | 「Show original」リンクの動作維持 | renderItems | — | — |
| 3.3 | タイトルリンクのスタイルをitem-cardと調和 | sidepanel.css | — | — |

## Components and Interfaces

| Component | Domain/Layer | Intent | Req Coverage | Key Dependencies | Contracts |
|-----------|--------------|--------|--------------|------------------|-----------|
| renderItems | UI / Sidepanel JS | 記事一覧のHTMLレンダリング（タイトルリンク追加） | 1.1, 1.2, 1.3, 1.4, 2.2, 3.1, 3.2 | getConfiguredAPIBase (P1), escapeHTML (P0) | — |
| sidepanel.css | UI / Stylesheet | タイトルリンクのスタイル定義 | 3.3 | — | — |

### UI / Sidepanel JS

#### renderItems（既存関数の変更）

| Field | Detail |
|-------|--------|
| Intent | 記事カードのタイトルをリンク付きでレンダリングする |
| Requirements | 1.1, 1.2, 1.3, 1.4, 2.1, 2.2, 3.1, 3.2 |

**Responsibilities & Constraints**
- 各記事の `item.id` を用いてWeb UI詳細URL `${apiBase}/ui/items/${item.id}` を構築する
- タイトルとURLの両方に `escapeHTML()` を適用する
- `apiBase` が空文字の場合、タイトルはリンクなしのプレーンテキストにフォールバックする
- `item.id` が欠損する場合もリンクなしにフォールバックする
- 既存の「Show original」リンクの構造と動作を変更しない

**Dependencies**
- Inbound: `fetchItems()` — レンダリング呼び出し (P0)
- Outbound: `getConfiguredAPIBase()` — Web UIベースURL取得 (P1)
- Outbound: `escapeHTML()` — XSSエスケープ処理 (P0)

**テンプレート変更の設計**

現行テンプレート:
```
<h3 class="item-title">${escapeHTML(title)}</h3>
```

変更後テンプレート（apiBaseが有効かつitem.idが存在する場合）:
```
<h3 class="item-title">
  <a href="${escapeHTML(detailURL)}" target="_blank" rel="noopener noreferrer">
    ${escapeHTML(title)}
  </a>
</h3>
```

変更後テンプレート（apiBaseが空またはitem.idが欠損の場合）:
```
<h3 class="item-title">${escapeHTML(title)}</h3>
```

**Implementation Notes**
- `getConfiguredAPIBase()` はループ外で1回だけ呼び出し、結果を変数に保持する
- `item.id` の存在チェック: `typeof item?.id === 'string' && item.id !== ''`
- URL構築: `${apiBase}/ui/items/${item.id}`（IDはUUID形式でURLセーフ。既存の `Show original` リンクと同様に `escapeHTML()` のみで処理する）

### UI / Stylesheet

#### sidepanel.css（スタイル追加）

| Field | Detail |
|-------|--------|
| Intent | タイトルリンクの視覚スタイルを既存デザインと調和させる |
| Requirements | 3.3 |

**追加CSSルール**

`.item-title a` セレクタで以下を定義:
- `color: inherit` — 既存の `--text-primary` カラーを継承
- `text-decoration: none` — 下線なし（プレーンテキストの見た目を維持）
- hover時: `color: var(--accent)` — アクセントカラーでクリッカブルであることを示す
- `cursor: pointer` は `<a>` タグのデフォルトで自動適用

## Error Handling

### Error Categories and Responses

**apiBase未設定**: タイトルをリンクなしのプレーンテキストとしてレンダリング（graceful degradation）。ユーザーの記事一覧閲覧に影響を与えない。

**item.id欠損**: 該当記事のタイトルのみリンクなし。他の記事には影響しない。

## Testing Strategy

### Unit Tests（`extension/sidepanel.test.mjs`）
1. 記事タイトルが `<a>` タグでラップされ、href が `${apiBase}/ui/items/${item.id}` であることを検証
2. タイトルリンクに `target="_blank"` と `rel="noopener noreferrer"` が含まれることを検証
3. タイトルが空の場合 "(untitled)" がリンクテキストとして表示されることを検証
4. 「Show original」リンクが変更されず維持されていることを検証（既存テスト `items list view renders title tags and original link only` の拡張）
5. apiBase未設定時にタイトルがリンクなしのプレーンテキストになることを検証
