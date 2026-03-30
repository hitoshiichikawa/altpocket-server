# Research & Design Decisions

## Summary
- **Feature**: `mcp-article-features`
- **Discovery Scope**: New Feature（MCP Server基盤の新規追加）
- **Key Findings**:
  - Go公式MCP SDK（`github.com/modelcontextprotocol/go-sdk` v1.4.1）が安定版として利用可能
  - 既存の`internal/store`パッケージがListItems/GetItemDetail/ListTagsWithCountを提供しており、MCP toolハンドラーから直接再利用可能
  - `GetUserByEmail`メソッドが未実装のため、MCP認証用に追加が必要

## Research Log

### Go MCP SDK選定
- **Context**: MCP Serverを実装するためのGoライブラリの調査
- **Sources Consulted**: GitHub（modelcontextprotocol/go-sdk, mark3labs/mcp-go）、pkg.go.dev
- **Findings**:
  - 公式SDK: `github.com/modelcontextprotocol/go-sdk` v1.4.1（2026-03-13リリース）、MCP v2025-11-25仕様対応
  - コミュニティ版: `github.com/mark3labs/mcp-go` v0.46.0、広く使われているが非公式
  - 公式SDKは型安全なジェネリックハンドラー（`AddTool[In, Out]`）を提供し、入力スキーマをGoの構造体タグから自動生成
  - stdioトランスポートは`mcp.StdioTransport{}`で簡潔に起動可能
- **Implications**: 公式SDKを採用。長期メンテナンス性とGoの型安全性を最大限活用可能

### 既存Store層の再利用性分析
- **Context**: MCP toolハンドラーが既存のDBアクセス層を再利用できるか
- **Findings**:
  - `store.ListItems(ctx, userID, page, perPage, q, tags, sort)` → list_items/search_itemsツールに直接対応
  - `store.GetItemDetail(ctx, userID, itemID)` → get_itemツールに直接対応
  - `store.ListTagsWithCountFiltered(ctx, userID, q, selectedTags)` → list_tagsツールに直接対応
  - 過去24h記事取得用のクエリは未実装 → store層に`ListRecentItems`メソッドの追加が必要
  - `GetUserByEmail`メソッドも未実装 → 追加が必要
- **Implications**: 大部分は既存store関数の薄いラッパーで実装可能。新規追加は2メソッドのみ

### MCP認証モデル
- **Context**: stdioトランスポートでの認証方式の検討
- **Findings**:
  - stdioはローカルプロセス間通信であり、ネットワーク認証は不要
  - ユーザーの特定は環境変数`MCP_USER_EMAIL`で行い、起動時にDB照合してuserIDを確定する方式が最適
  - 既存の`config.Load()`はAPIサーバー向けのmustEnvが多く、MCP用には別のconfig関数が必要
- **Implications**: MCP専用の軽量config（DATABASE_URL + MCP_USER_EMAIL）を用意

## Architecture Pattern Evaluation

| Option | Description | Strengths | Risks / Limitations | Notes |
|--------|-------------|-----------|---------------------|-------|
| 独立バイナリ（cmd/mcp） | 既存api/workerと同列の第3エントリーポイント | 既存パターンとの一貫性、既存store層を直接共有 | バイナリが増える | 既存のcmd/api, cmd/worker構成に自然に適合 |
| APIサーバーへのMCPエンドポイント追加 | 既存HTTPサーバーにMCP over SSEを追加 | バイナリ数が増えない | HTTP認証が必要、stdioと非互換 | MCPクライアントの標準接続方式と合わない |

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

### Decision: 独立バイナリ（cmd/mcp）としての配置
- **Context**: MCP Serverの実行形態
- **Selected Approach**: `cmd/mcp/main.go`として独立バイナリ化
- **Rationale**: 既存の`cmd/api`・`cmd/worker`パターンとの一貫性。stdioトランスポートはプロセス起動型で、HTTPサーバーとは根本的に異なる
- **Trade-offs**: Dockerイメージの追加が必要だが、構造の明確さが勝る

### Decision: 環境変数ベースのユーザー特定
- **Context**: stdioトランスポートでの認証方式
- **Selected Approach**: `MCP_USER_EMAIL`環境変数で対象ユーザーを指定し、起動時にDB照合
- **Rationale**: stdioはローカルプロセスなのでネットワーク認証不要。MCPクライアント設定で環境変数を指定する形が最もシンプル
- **Trade-offs**: マルチユーザー対応は不可だが、セルフホスト前提では十分

## Risks & Mitigations
- 公式SDKのAPIが今後変更される可能性 → v1安定版を使用し、go.sumでバージョン固定
- 過去24h記事が大量の場合のレスポンスサイズ → ページネーション不要（リソースは一括取得が前提）だが、件数が多い場合は概要のみ返却
- DB接続失敗時のMCPサーバーの振る舞い → 起動時にpingで確認し、失敗時は即座にexit

## References
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) — 公式Go SDK
- [MCP仕様 v2025-11-25](https://spec.modelcontextprotocol.io/) — プロトコル仕様
- [pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp) — APIドキュメント
