# pocket-compat (altpocket)

Pocket互換の「あとで読む」サービス。Chrome ExtensionでURL+タグを保存し、Web UIで一覧/検索/タグ絞り込み/詳細閲覧/削除/再フェッチができます。本文取得は非同期workerが毎分実行します。

## 構成
- API + Web UI(SSR): `cmd/api`
- Worker: `cmd/worker`
- DB: PostgreSQL 16
- Migration: `migrations/001_init.sql`
- Extension: `extension/`

## 必須環境変数
```
POSTGRES_USER=altpocket
POSTGRES_PASSWORD=altpocket
POSTGRES_DB=altpocket
DATABASE_URL=postgres://altpocket:altpocket@db:5432/altpocket?sslmode=disable
PUBLIC_BASE_URL=http://localhost:8080
SESSION_SECRET=change-me
JWT_SECRET=change-me
GOOGLE_WEB_CLIENT_ID=your-web-client-id
GOOGLE_CLIENT_SECRET=your-web-client-secret
GOOGLE_EXT_CLIENT_ID=your-extension-client-id
```

### Google OAuth 設定
- Web: OAuth同意画面 + WebクライアントIDを作成し、リダイレクトURIに `http://localhost:8080/v1/auth/google/callback` を登録
- Extension: Chrome拡張用のOAuthクライアントIDを作成（Webとは別ID）

## ローカル起動 (Docker Compose)
```
docker compose up -d db
psql "postgres://altpocket:altpocket@localhost:5432/altpocket?sslmode=disable" -f migrations/001_init.sql

docker compose up --build api worker
```

Web UI: http://localhost:8080/ui/items

> セッションはDB保存です（`sessions`テーブル）。worker が毎分、期限切れセッションを削除します。

## 拡張機能のロード
1. `extension/popup.js` の `CLIENT_ID` を自分のExtension用OAuthクライアントIDに置換
2. Chromeの拡張機能管理画面で「パッケージ化されていない拡張機能を読み込む」→ `extension/` を選択
3. PopupでAPI Base URLを入力し、Login → Save Current Tab

## API概要
- `POST /v1/items` {url,tags[]} -> 200 {item_id, created}
- `GET /v1/items` page/per_page/q/tag/sort
- `GET /v1/items/:id`
- `DELETE /v1/items/:id`
- `POST /v1/items/:id/refetch`
- `GET /v1/tags?q=`
- `POST /v1/auth/extension/exchange` {id_token}

## 開発コマンド
```
go test ./...
```

## Extensionテスト
```
node --test extension/popup.test.mjs
```

## APIスモークテスト
```
API_BASE=http://localhost:8080 ./scripts/test-api.sh
```

PowerShell 7+ (Windows):
```
$env:API_BASE = "http://localhost:8080"
.\scripts\test-api.ps1
```

詳細は `docs/smoke-test.md` を参照してください。

## 本番デプロイ（Docker）
- Linux向け: `docs/production-docker-deploy.md`
- Windows向け: `docs/production-docker-deploy-windows.md`

## セキュリティ/運用メモ
- JWT署名キー/セッションシークレットは必ず環境変数で管理
- OAuthクライアントID/Secretは秘匿扱い
- ログはJSONでstdoutに出力されます（トークン等は出力しません）
