# Project Structure

_updated_at: 2026-02-24_

## Organization Philosophy

実行境界（`cmd`）とドメイン/インフラ実装（`internal`）を分離したレイヤード構成を採用します。  
API/Worker/Extension/UI は入口が異なっても、保存・認証・取得の中核ルールは共有パッケージで一貫させます。

## Directory Patterns

### Entrypoints
**Location**: `/cmd/`  
**Purpose**: 実行プロセスの起動と依存注入（API と Worker の組み立て）  
**Example**: `cmd/api/main.go`, `cmd/worker/main.go`

### Core Application Packages
**Location**: `/internal/`  
**Purpose**: 認証、HTTP 層、DB 操作、取得ロジックなどの再利用可能なアプリ本体  
**Example**: `internal/server`, `internal/store`, `internal/fetcher`

### SSR Presentation Assets
**Location**: `/templates/`, `/static/`  
**Purpose**: Web UI の HTML テンプレートと静的アセット  
**Example**: `templates/items.html`, `static/app.js`

### External Client Surface
**Location**: `/extension/`  
**Purpose**: Chrome Extension (MV3) の UI/認証/保存クライアント実装  
**Example**: `extension/sidepanel.js`, `extension/manifest.json`

### Data & Operations
**Location**: `/migrations/`, `/deploy/`, `/scripts/`, `/docs/`  
**Purpose**: スキーマ進化、デプロイ定義、スモークテスト、運用手順の管理  
**Example**: `migrations/001_init.sql`, `deploy/docker-compose.production.yml`

## Naming Conventions

- **Files**: 小文字ベース、複合語は `snake_case`（例: `quick_add.html`, `json_tags_test.go`）
- **Packages**: 短い小文字名（例: `server`, `store`, `urlnorm`）
- **Functions**: 公開 API は `PascalCase`、内部補助は `camelCase`、HTTP ハンドラーは `handle*` を基本とする
- **Data Contracts**: SQL オブジェクト名と JSON フィールド名は `snake_case` を優先する

## Import Organization

```go
import (
	// 1) 標準ライブラリ
	"net/http"

	// 2) プロジェクト内部
	"altpocket/internal/store"

	// 3) サードパーティ
	"github.com/go-chi/chi/v5"
)
```

**Path Aliases**:
- Go の標準 import パスを使用し、独自エイリアスは導入しない

## Code Organization Principles

- HTTP ハンドラーは認証・バリデーション・レスポンス整形を担当し、永続化ロジックを直接持たない
- DB 更新は `internal/store` に集中させ、呼び出し側はユースケース単位で操作する
- 共有ルール（正規化、レート制御、ログ、設定）は独立パッケージ化して横断利用する
- 新規機能は「既存レイヤーを崩さず追加できること」を優先し、同一パターンならステアリング更新不要を原則とする

---
_ファイル一覧ではなく、追加実装時に迷わない構造ルールを記載する_
