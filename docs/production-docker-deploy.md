# 本番向け Docker デプロイ手順（インターネット公開）

この手順は、単一サーバーで `db` + `api` + `worker` を Docker Compose で動かし、`Caddy` 経由で HTTPS 公開するための運用手順です。

本手順は次の構成（方式1）を前提にしています。
- Web/UI ドメイン: `www.example.invalid`
- API ドメイン: `api.example.invalid`
- 実体は同じ `api` コンテナ（コード変更なし）

## 0. 前提
- Ubuntu 等の Linux サーバー 1 台
- ドメインを所有し、`www` と `api` の DNS A レコードをサーバーIPに向けられる
- サーバーで `docker` / `docker compose` が使える
- サーバーの inbound は最低限 `22/tcp`, `80/tcp`, `443/tcp` のみ許可

## 1. Google OAuth を本番値へ変更
本番公開前に Google Cloud 側で以下を作成/更新します。

1. OAuth 同意画面を設定（本番ドメインを反映）
2. Web クライアント ID
   - Redirect URI: `https://<WWWドメイン>/v1/auth/google/callback`
3. Chrome Extension クライアント ID
   - 本番で使う拡張機能IDに紐づける

## 2. 本番用 env ファイル作成
テンプレートをコピーして本番値に置換します。

```bash
cp deploy/.env.production.example deploy/.env.production
```

必須で置換する値:
- `WWW_HOSTNAME`
- `API_HOSTNAME`
- `PUBLIC_BASE_URL` (`https://<WWWドメイン>`)
- `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`
- `DATABASE_URL`
- `SESSION_SECRET` / `JWT_SECRET`
- `GOOGLE_WEB_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` / `GOOGLE_EXT_CLIENT_ID`

シークレットは以下で生成できます。

```bash
openssl rand -hex 32
```

注意:
- `deploy/.env.production` は機密情報を含むため Git へコミットしない
- `POSTGRES_PASSWORD` はローカルのデフォルト値を使い回さない

## 3. 本番 compose ファイルをルートへコピー
本番用 compose ファイルをリポジトリルートへコピーします。

```bash
cp deploy/docker-compose.production.yml ./docker-compose.production.yml
```

## 4. 本番 compose 構成のポイント
以下のファイルを使います。
- `docker-compose.production.yml`（`deploy/docker-compose.production.yml` をコピーしたもの）
- `deploy/Caddyfile.production`

この構成で以下を実現します。
- DB ポートの外部公開を無効化 (`db` の `ports: []`)
- `api` は外部公開せず、`edge`(Caddy) からのみ到達
- `edge` が `80/443` を待ち受け、`www` と `api` の両ホストを `api:8080` へリバースプロキシ
- `www` からも `/v1/*` は到達可能（方式1では許容）

## 5. 初回デプロイ
```bash
# 1) DBだけ先に起動
docker compose \
  --env-file deploy/.env.production \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  up -d db

# 2) マイグレーション適用
docker compose \
  --env-file deploy/.env.production \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" < migrations/001_init.sql

# 3) API/worker/edge 起動
docker compose \
  --env-file deploy/.env.production \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  up -d --build api worker edge
```

## 6. 動作確認
### 6.1 ヘルスチェック
```bash
curl -i https://<APIドメイン>/healthz
curl -i https://<WWWドメイン>/healthz
```
`200` と `ok` を確認。

### 6.2 API smoke test
サーバー上でリポジトリのルートから実行:

```bash
API_BASE=https://<APIドメイン> ./scripts/test-api.sh
```

### 6.3 Extension E2E
1. Web で `https://<WWWドメイン>/v1/auth/google/login` にアクセスして一度ログイン（ユーザー登録）
2. `extension/popup.js` の `CLIENT_ID` を本番用 extension client id にする
3. 拡張機能の API Base URL を `https://<APIドメイン>` にしてログイン
4. 任意ページで Save し、`https://<WWWドメイン>/ui/items` に反映されることを確認

## 7. 更新デプロイ
```bash
git pull

docker compose \
  --env-file deploy/.env.production \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  up -d --build api worker edge
```

## 8. バックアップ（最低限）
```bash
docker compose \
  --env-file deploy/.env.production \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  exec -T db pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB" > backup_$(date +%F).sql
```

## 9. 運用上の注意
- OAuth クライアント情報、`SESSION_SECRET`、`JWT_SECRET` はローテーション方針を決める
- `docker compose logs -f api worker edge` で監視し、異常時に即検知できるようにする
- OS / Docker / イメージ更新を定期実施する
- 本番で `db` ポートを公開しない
