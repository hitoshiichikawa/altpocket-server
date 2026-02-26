# Technology Stack

_updated_at: 2026-02-26_

## Architecture

altpocket は Go モノレポで、`api` と `worker` の 2 実行バイナリを分離した構成です。  
HTTP API / SSR UI は同期リクエスト処理、本文抽出は非同期ワーカー処理として分離し、共有データストアとして PostgreSQL を使用します。

## Core Technologies

- **Language**: Go 1.22
- **Framework**: chi v5（HTTP ルーティング）、Go 標準 `html/template`（SSR）
- **Runtime**: Go バイナリ実行 + Docker Compose（ローカル/本番構成）
- **Database**: PostgreSQL 16（pgx/v5）

## Key Libraries

- `github.com/go-chi/chi/v5`: ルーティングとミドルウェア連携
- `github.com/jackc/pgx/v5`: DB 接続・クエリ実行
- `github.com/PuerkitoBio/goquery`: 本文抽出の HTML 解析
- `golang.org/x/oauth2` + `google.golang.org/api`: Google OAuth / Sheets 連携
- `log/slog`: JSON 構造化ログ

## Development Standards

### Type Safety

- Go の静的型を前提に、API レスポンスは JSON タグで `snake_case` を維持する
- URL/タグ正規化のような境界処理は専用パッケージに分離して扱う

### Code Quality

- `gofmt` を標準フォーマットとする
- ハンドラーは入力検証とオーケストレーションに集中し、DB 操作は `internal/store` に集約する
- ログは `slog` の構造化ログを利用し、機密情報を出力しない

### Testing

- Go テストは `go test ./...` を基本とし、正規化ロジックはテーブル駆動テストを優先する
- 拡張機能は `node --test extension/sidepanel.test.mjs` を基本とし、必要に応じて手動E2E（login/save/search/logout）を補完する
- 拡張機能 API の契約テスト（`extension_contract_test.go`）でサーバー側レスポンス形式の互換性を保証する
- CI は `go test`・`golangci-lint`・Docker build で最小品質ゲートを構成する

## Development Environment

### Required Tools

- Go 1.22+
- PostgreSQL 16
- Docker / Docker Compose
- Node.js（拡張機能テスト実行時）

### Common Commands

```bash
# Dev (API + Worker + DB)
docker compose up --build api worker

# Test (Go)
go test ./...

# Test (Extension)
node --test extension/sidepanel.test.mjs
```

## Key Technical Decisions

- 保存処理と本文取得処理を分離し、UX と取得安定性を両立する
- SSR + Vanilla JS を採用し、依存を増やさず配布・運用コストを抑える
- 認証は Web セッション（DB）と拡張機能トークンの二系統を用途別に使い分ける
- CORS 許可リスト（`CORS_ALLOW_ORIGINS`）でオリジン制御し、拡張機能と Web UI の安全な通信を実現する
- 検索体験は PostgreSQL の trigram index と正規化データで実現する

---
_全依存列挙ではなく、開発判断に効く技術選定と規約を記載する_
