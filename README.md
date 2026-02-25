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
CORS_ALLOW_ORIGINS=chrome-extension://your-extension-id,http://localhost:8080
```

`APP_ENV=production` では `CORS_ALLOW_ORIGINS` が必須です（未設定だと起動時にpanicします）。

### Google OAuth 設定
- Google Cloud Console の「API とサービス > ライブラリ」で `Google Sheets API` を有効化
- Web: OAuth同意画面 + WebクライアントIDを作成し、リダイレクトURIに `http://localhost:8080/v1/auth/google/callback` を登録
- Google Sheets Export を使う場合は、同じ Web クライアントに `http://localhost:8080/ui/settings/google/callback` も追加
- Extension: Chrome拡張用のOAuthクライアントIDを作成（Webとは別ID）

## ローカル起動 (Docker Compose)
```
docker compose up -d db
psql "postgres://altpocket:altpocket@localhost:5432/altpocket?sslmode=disable" -f migrations/001_init.sql
psql "postgres://altpocket:altpocket@localhost:5432/altpocket?sslmode=disable" -f migrations/002_google_sheets_connections.sql

docker compose up --build api worker
```

Web UI: http://localhost:8080/ui/items

> セッションはDB保存です（`sessions`テーブル）。worker が毎分、期限切れセッションを削除します。

## 拡張機能のロード
1. `extension/sidepanel.js` の `CLIENT_ID` を自分のExtension用OAuthクライアントIDに置換
2. `extension/sidepanel.js` の `API_BASE` を固定のAPIオリジンに設定
3. Chromeの拡張機能管理画面で拡張機能IDを確認し、`CORS_ALLOW_ORIGINS` に `chrome-extension://<拡張機能ID>` を設定して API を再起動
4. Chromeの拡張機能管理画面で「パッケージ化されていない拡張機能を読み込む」→ `extension/` を選択
5. 拡張機能アイコンをクリックしてSide Panelを開き、Login → Save Current Tab
6. Save時はまずURL/タグだけを保存し、その後拡張機能が抽出した本文を非同期で `POST /v1/items/:id/capture` に送信します（保存操作は待たない）。この本文はworker fetch失敗時のフォールバックとして扱われ、worker fetch成功時はworker側の本文が優先されます。

## モバイル登録導線

### Android (Chrome): PWA Share Target
1. `https://YOUR_DOMAIN/ui/items` でGoogleログインします（Webセッション必須）。
2. Chromeメニューから「ホーム画面に追加」で altpocket をインストールします。
3. 保存したいページで「共有」→ `altpocket` を選択します。
4. `Quick Add` 画面が開くので、内容を確認して保存します。

### iPhone (Safari): ショートカット共有導線
1. `https://YOUR_DOMAIN/ui/items` でGoogleログインします（Webセッション必須）。
2. iOSショートカットを作成し、共有シートから受け取る入力を `URL` に設定します。
3. アクション「URLを開く」で `https://YOUR_DOMAIN/ui/quick-add?url=<共有されたURL>` を開くよう設定します。
4. Safariの共有シートから作成したショートカットを実行し、`Quick Add` 画面で保存します。

### Bookmarklet（デスクトップ向け）
以下をブックマークURLに設定すると、`Quick Add` を別タブで開きます。URL/タイトルに加えて、本文候補（最大200文字）を `content` として渡します。元ページは遷移しません。
`content` は worker fetch が失敗したときのフォールバック用途で、worker fetch が成功した場合は worker 側の本文が優先されます。

```text
javascript:(()=>{const base='https://YOUR_DOMAIN/ui/quick-add';const u=new URL(base);u.searchParams.set('url',location.href);u.searchParams.set('title',document.title||'');const normalize=v=>v.trim().replace(/\s+/g,' ');const truncate=(v,max)=>v.length>max?v.slice(0,max):v;const drop=['script','style','noscript','template','svg','canvas','iframe','nav','aside','footer','form','[hidden]','[aria-hidden=\"true\"]'];const source=document.querySelector('article, main, [role=\"main\"]')||document.body;if(source){const clone=source.cloneNode(true);drop.forEach(s=>clone.querySelectorAll(s).forEach(n=>n.remove()));const preview=truncate(normalize(clone.innerText||clone.textContent||''),200);if(preview){u.searchParams.set('content',preview);}}window.open(u.toString(),'_blank','noopener,noreferrer');})();
```

## API概要
- `POST /v1/items` {url,tags[]} -> 200 {item_id, created}
- `POST /v1/items/:id/capture` {title,content_full} -> 204（拡張機能の非同期本文アップロード）
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
現状、Side Panel向けの自動テストは未整備です。`go test ./...` と手動E2Eで確認してください。

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
