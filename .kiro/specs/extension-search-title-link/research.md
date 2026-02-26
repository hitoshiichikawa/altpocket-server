# Research & Design Decisions

## Summary
- **Feature**: `extension-search-title-link`
- **Discovery Scope**: Extension（既存UIの拡張）
- **Key Findings**:
  - `renderItems()` 内のHTMLテンプレートを1箇所変更するだけで要件を満たせる
  - APIレスポンスに `item.id` が既に含まれており、サーバー側変更は不要
  - `openWebUI()` が `${apiBase}/ui/items` パターンを確立しており、同パターンで詳細URLを構築可能

## Research Log

### renderItems のテンプレート構造
- **Context**: タイトルをリンク化するための既存テンプレート構造を調査
- **Sources Consulted**: `extension/sidepanel.js` L408-434
- **Findings**:
  - タイトルは `<h3 class="item-title">${escapeHTML(title)}</h3>` としてレンダリング
  - `item.id` はAPIレスポンスに含まれるが、`renderItems()` では未使用
  - `escapeHTML()` が全ユーザーデータに適用される慣例
- **Implications**: `<h3>` 内に `<a>` タグを追加し、`item.id` を使ってhrefを構築する

### WebベースURLの導出パターン
- **Context**: 拡張機能からWeb UI URLをどう構築するか
- **Sources Consulted**: `extension/sidepanel.js` L99-113, L676-692
- **Findings**:
  - `getConfiguredAPIBase()` が `API_BASE` からoriginを返す（例: `https://api.example.test`）
  - `openWebUI()` が `${apiBase}/ui/items` を構築して新タブで開く既存パターン
  - APIサーバーとWeb UIは同一originで提供される
- **Implications**: `renderItems()` に `apiBase` を渡し、`${apiBase}/ui/items/${item.id}` を構築する

### apiBaseの伝搬方法
- **Context**: `renderItems()` がAPIベースURLにアクセスする方法
- **Sources Consulted**: `extension/sidepanel.js` 全体の関数シグネチャ
- **Findings**:
  - `renderItems(items)` は引数が `items` 配列のみ
  - 呼び出し元 `fetchItems()` は `apiBase` をローカル変数で保持（L439）
  - `getConfiguredAPIBase()` はグローバル関数として常に呼び出し可能
- **Implications**: `renderItems()` 内で直接 `getConfiguredAPIBase()` を呼ぶか、引数を追加するかの選択肢がある

## Design Decisions

### Decision: apiBaseの取得方法
- **Context**: `renderItems()` でWeb UI URLを構築するためにapiBaseが必要
- **Alternatives Considered**:
  1. `renderItems()` に `apiBase` 引数を追加 — 明示的だが全呼び出し元の変更が必要
  2. `renderItems()` 内で `getConfiguredAPIBase()` を直接呼出し — 変更箇所が最小
- **Selected Approach**: `getConfiguredAPIBase()` を `renderItems()` 内で直接呼び出す
- **Rationale**: `getConfiguredAPIBase()` は副作用のない純関数で、グローバルスコープで利用可能。引数追加は呼び出し元2箇所（`fetchItems` L476、`saveCurrentTab` L664）の変更を伴い、テストへの影響も大きい
- **Trade-offs**: 関数の暗黙的依存が増えるが、既に `openWebUI()` など他の関数も同様のパターンを使用しており一貫性がある
- **Follow-up**: apiBaseが空文字の場合のフォールバック（リンクなし or `#`）を実装時に確認

### Decision: タイトルリンクのCSS戦略
- **Context**: リンク化してもデザインの一貫性を保つ必要がある
- **Alternatives Considered**:
  1. `.item-title a` セレクタで `color: inherit; text-decoration: none` — 既存の見た目を完全維持
  2. アクセント色によるリンク表示 — クリッカブルであることを明示
- **Selected Approach**: `color: inherit; text-decoration: none` + hover時に微妙な変化
- **Rationale**: 既存の `.item-title` スタイルとの調和を最優先。hover時のフィードバックでクリッカブルであることを示す
- **Trade-offs**: リンクであることが初見では分かりにくいが、hoverフィードバックで補完できる

## Risks & Mitigations
- **apiBase未設定時**: リンクが無効になる可能性 → apiBase空文字時はリンクなしのプレーンテキストにフォールバック
- **item.idが欠損するケース**: APIレスポンスにidが含まれない想定外のケース → 防御的にidチェックを追加

## References
- `extension/sidepanel.js` — 拡張機能サイドパネルのメインスクリプト
- `extension/sidepanel.css` — サイドパネルのスタイルシート
- `extension/sidepanel.test.mjs` — テストスイート
- `internal/server/server.go` L140 — Web UI詳細ルート定義 `/ui/items/{id}`
