# ローカルDockerデプロイ手順

この手順は **Docker Compose** で `db` + `api` + `worker` を起動し、ローカルで動作確認するためのガイドです。

## 前提
- Docker Desktop（またはDocker Engine）をインストール済み
- `docker compose` が使えること

## 1. 環境変数の準備
`docker-compose.yml` に必要な環境変数が定義されています。Google OAuthを使う場合は、以下を **実値に変更** してください。

- `GOOGLE_WEB_CLIENT_ID`
- `GOOGLE_CLIENT_SECRET`
- `GOOGLE_EXT_CLIENT_ID`
- `CORS_ALLOW_ORIGINS`（`chrome-extension://<拡張機能ID>,http://localhost:8080`）
- `PUBLIC_BASE_URL`
- `SESSION_SECRET` / `JWT_SECRET`

> OAuth を使わない場合でも起動は可能ですが、ログイン/保存系APIは利用できません。

## 2. DB起動
```bash
docker compose up -d db
```

## 3. マイグレーション適用
**方法A: ホストに `psql` がある場合**
```bash
psql "postgres://altpocket:altpocket@localhost:5432/altpocket?sslmode=disable" -f migrations/001_init.sql
```

**方法B: `psql` がない場合（コンテナ経由）**
```bash
docker compose exec -T db psql -U altpocket -d altpocket < migrations/001_init.sql
```

## 4. API / Worker 起動
```bash
docker compose up --build api worker
```

- API: http://localhost:8080
- UI: http://localhost:8080/ui/items
- ヘルスチェック: http://localhost:8080/healthz

## 5. 動作確認
- UIにアクセスし、Googleログインが完了できること
- `/v1/items` 系APIが使えること

## 6. 停止/クリーンアップ
```bash
# 停止
docker compose down

# DBを含め全削除（データ初期化）
docker compose down -v
```

## 7. （任意）Chrome拡張で動作確認
1. `extension/sidepanel.js` の `CLIENT_ID` をChrome拡張用のOAuthクライアントIDに置換
2. `extension/sidepanel.js` の `API_BASE` を `http://localhost:8080` に設定
3. Chromeの拡張機能管理画面で拡張機能IDを確認し、`CORS_ALLOW_ORIGINS` に `chrome-extension://<拡張機能ID>` を含めて API を再起動
4. Chromeの拡張機能管理画面で「パッケージ化されていない拡張機能を読み込む」→ `extension/` を選択
5. 拡張機能アイコンをクリックして Side Panel を開く
6. Login → Save Current Tab
