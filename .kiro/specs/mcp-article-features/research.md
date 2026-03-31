# Research & Design Decisions

## Summary
- **Feature**: `mcp-article-features`
- **Discovery Scope**: New Feature（MCP Server基盤の新規追加）
- **Key Findings**:
  - Go公式MCP SDK（`github.com/modelcontextprotocol/go-sdk` v1.4.1）が安定版として利用可能。Streamable HTTP transport内蔵
  - 既存の`internal/store`パッケージがListItems/GetItemDetail/ListTagsWithCountを提供しており、MCP toolハンドラーから直接再利用可能
  - 既存APIサーバーへのエンドポイント埋め込みが最もシンプルな構成（新規バイナリ/コンテナ不要）

## Research Log

### Go MCP SDK選定
- **Context**: MCP Serverを実装するためのGoライブラリの調査
- **Sources Consulted**: GitHub（modelcontextprotocol/go-sdk, mark3labs/mcp-go）、pkg.go.dev
- **Findings**:
  - 公式SDK: `github.com/modelcontextprotocol/go-sdk` v1.4.1（2026-03-13リリース）、MCP v2025-11-25仕様対応
  - コミュニティ版: `github.com/mark3labs/mcp-go` v0.46.0、広く使われているが非公式
  - 公式SDKは型安全なジェネリックハンドラー（`AddTool[In, Out]`）を提供し、入力スキーマをGoの構造体タグから自動生成
  - Streamable HTTP transportは`mcp.NewStreamableHTTPServer(server)`で生成し、`http.Handler`として利用可能
- **Implications**: 公式SDKを採用。Streamable HTTP transportの`http.Handler`をchiルーターにマウント

### 既存Store層の再利用性分析
- **Context**: MCP toolハンドラーが既存のDBアクセス層を再利用できるか
- **Findings**:
  - `store.ListItems(ctx, userID, page, perPage, q, tags, sort)` → list_items/search_itemsツールに直接対応
  - `store.GetItemDetail(ctx, userID, itemID)` → get_itemツールに直接対応
  - `store.ListTagsWithCountFiltered(ctx, userID, q, selectedTags)` → list_tagsツールに直接対応
  - 過去24h記事取得用のクエリは未実装 → store層に`ListRecentItems`メソッドの追加が必要
  - `GetUserByEmail`メソッドも未実装 → 追加が必要
- **Implications**: 大部分は既存store関数の薄いラッパーで実装可能。新規追加は2メソッドのみ

### MCP認証モデル（HTTP transport）
- **Context**: インターネット経由でのMCPアクセスにおける認証方式の検討
- **Findings**:
  - Streamable HTTP transportではネットワーク認証が必須
  - Bearer Token（APIキー）方式が最もシンプルで、MCPクライアントのHTTPヘッダー設定と相性が良い
  - `MCP_API_KEY`環境変数でトークンを管理し、`MCP_USER_EMAIL`でデータスコープを特定
  - 両環境変数が未設定の場合はMCPエンドポイント自体を無効化（既存APIに影響なし）
  - 既存の`config.Load()`に`MCPConfig`を追加する形で統合可能
- **Implications**: chiミドルウェアでBearer Token検証を行い、認証済みリクエストのみMCP SDKに転送

### トランスポート方式の比較
- **Context**: stdio vs HTTP (Streamable HTTP) の選択
- **Findings**:
  - stdio: MCPクライアントがサブプロセスとして起動。ローカル実行が前提。DB接続URLをクライアント側に配置する必要あり
  - Streamable HTTP: HTTPエンドポイントとして公開。リモートアクセス可能。既存APIサーバーに統合可能
  - セルフホスト環境ではMCPクライアント（Claude Desktop等）とサーバーが別マシンの場合が一般的
  - Streamable HTTPは公式SDKの`StreamableHTTPServer`で`http.Handler`を生成でき、chiルーターにマウント可能
- **Implications**: Streamable HTTP transportを採用。既存APIサーバーの`/mcp`パスにマウント

## Architecture Pattern Evaluation

| Option | Description | Strengths | Risks / Limitations | Notes |
|--------|-------------|-----------|---------------------|-------|
| 既存APIサーバーへの埋め込み | chiルーターに/mcpパスとしてマウント | 新規バイナリ/コンテナ不要、既存インフラ活用、デプロイ変更なし | APIサーバーの責務が増える | **採用**: 最もシンプルかつ運用コスト最小 |
| 独立バイナリ（cmd/mcp） | 第3エントリーポイント | 関心の分離 | 新規Dockerサービス必要、ポート管理、認証の二重管理 | HTTPの場合はメリット薄い |
| 独立HTTPサーバー（別ポート） | 別プロセスでMCP専用サーバー | 完全な分離 | Docker Composeの変更、リバースプロキシ設定追加 | 過剰な分離 |

## Design Decisions

### Decision: Go公式MCP SDKの採用
- **Context**: MCP Server実装に使用するライブラリの選択
- **Alternatives Considered**:
  1. 公式SDK（`modelcontextprotocol/go-sdk`） — 安定版v1+、型安全ジェネリクス
  2. mark3labs/mcp-go — コミュニティ標準、ビルダーパターンAPI
  3. 自前実装 — JSON-RPC 2.0を直接実装
- **Selected Approach**: 公式SDK v1.4.1
- **Rationale**: v1安定版リリース済み、Goの型安全性を活用したジェネリックハンドラー、長期メンテナンスの信頼性
- **Trade-offs**: mark3labs/mcp-goの方がコミュニティ事例が多いが、公式SDKの方が仕様追従が確実
- **Follow-up**: go.modへの依存追加、最新バージョン互換性の確認

### Decision: 既存APIサーバーへのエンドポイント埋め込み
- **Context**: MCP Serverの実行形態（方針修正: stdio→HTTP）
- **Selected Approach**: 既存`cmd/api`の chiルーターに`/mcp`パスとしてマウント
- **Rationale**: インターネット経由のアクセスが前提。既存APIサーバーに埋め込むことで、新規バイナリ・Dockerサービス・リバースプロキシ設定が不要。デプロイフローの変更もなし
- **Trade-offs**: APIサーバーの責務が若干増えるが、MCPハンドラーは`internal/mcp`パッケージに分離されており影響は限定的

### Decision: DB保存APIキー + 設定画面管理
- **Context**: インターネット経由でのMCPアクセス認証方式
- **Alternatives Considered**:
  1. 環境変数`MCP_API_KEY` — 最もシンプルだがローテーション時にサーバー再起動必要
  2. 設定画面でAPIキー生成・DB保存 — UXが良く、複数キー管理・即時失効可能
  3. 既存JWT認証の流用 — MCPクライアントのトークンリフレッシュが煩雑
- **Selected Approach**: 設定画面からAPIキーを生成し、SHA-256ハッシュをDBに保存
- **Rationale**: ユーザーが自身でキーを管理でき、即時失効・複数キー対応が可能。サーバー再起動不要
- **Trade-offs**: DBテーブル追加・マイグレーションが必要だが、運用の柔軟性が大幅に向上
- **Security**: 平文キーはDB保存せず、生成時に1回のみ表示。SHA-256ハッシュで照合

## Risks & Mitigations
- 公式SDKのAPIが今後変更される可能性 → v1安定版を使用し、go.sumでバージョン固定
- 過去24h記事が大量の場合のレスポンスサイズ → 概要のみ返却（全文は含めない）
- APIキー漏洩リスク → HTTPS前提の運用ドキュメント整備、ログにAPIキーを含めない
- MCP SDKのHTTP handlerとchiルーターの互換性 → `http.Handler`インターフェースで標準互換

## References
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) — 公式Go SDK
- [MCP仕様 v2025-11-25](https://spec.modelcontextprotocol.io/) — プロトコル仕様
- [pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp) — APIドキュメント
