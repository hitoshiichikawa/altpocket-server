# Docker API Smoke Test

## Purpose
Run an end-to-end smoke test against the local Docker Compose stack over `http://localhost:8080`.

## Prerequisites
- Docker daemon is running
- Services are started:

```bash
docker compose up -d db
docker compose exec -T db psql -U altpocket -d altpocket < migrations/001_init.sql
docker compose up --build -d api worker
```

## Run
```bash
API_BASE=http://localhost:8080 ./scripts/test-api.sh
```

Windows (PowerShell 7+):
```powershell
$env:API_BASE = "http://localhost:8080"
.\scripts\test-api.ps1
```

When you run with non-default compose files or env-file, set `COMPOSE_CMD` as well:
```bash
API_BASE=https://<APIドメイン> \
COMPOSE_CMD='docker compose --env-file deploy/.env.production -f docker-compose.yml -f deploy/docker-compose.production.yml' \
./scripts/test-api.sh
```

## Credential Strategy (implemented)
`./scripts/get-test-credentials.sh` acquires test credentials without external OAuth dependency:
- Inserts a temporary test user into PostgreSQL
- Creates a temporary web session (`altpocket_session`) + CSRF token
- Issues an HS256 JWT for the same user (uses `JWT_SECRET`; derives from `docker compose config` when not explicitly set)
- Exports shell variables used by `./scripts/test-api.sh`

PowerShell variant uses `./scripts/get-test-credentials.ps1` and `./scripts/test-api.ps1`.

The smoke test uses both auth paths:
- Session cookie + CSRF header
- Bearer JWT

By default, the temporary test user data is deleted automatically at script exit.
Set `KEEP_TEST_DATA=1` to keep it for debugging.

## Google Sheets `refresh_token` 暗号化のスモーク確認（Issue #81）

`refresh_token` が DB に平文で残っていないこと、および鍵差し替え時に既存接続が
「未接続相当」に合流して再認可フローへ誘導されることを手動で確認する手順です。
本機能は `Req 4.4` / `Req 2.3` のシナリオを覆います。

### 前提

- `migrations/001_init.sql` 〜 `005_invalidate_legacy_refresh_tokens.sql` を全て適用済み
- `ENCRYPTION_KEY` を `openssl rand -base64 32` で生成して `.env` に投入済み
- API（`cmd/api`）が起動済み

### 手順

1. **新規接続で暗号化を確認**
   1. ブラウザで `/ui/settings` を開き Google ログイン
   2. 「Connect Google account」ボタンから OAuth 同意して Google Sheets 連携を作成
   3. 「Export to Google Sheets」を実行し、初回エクスポート成功 → スプレッドシートが
      作成されることを確認
2. **DB ダンプで暗号文を目視確認**
   ```bash
   psql "$DATABASE_URL" -c "SELECT user_id, length(refresh_token) AS len, substring(refresh_token, 1, 16) AS head FROM user_google_sheets_connections;"
   ```
   - `head` 列が `1//` で始まっていない（平文 OAuth refresh_token のシグネチャと
     一致しない）こと
   - `len` が概ね 80 文字以上（base64 化された nonce + ciphertext + tag 相当）
     であること
3. **鍵差し替えで再認可フローを確認**
   1. `ENCRYPTION_KEY` を別の有効な base64 32 バイト鍵に差し替えて API を再起動
   2. `/ui/settings` を再読み込みすると、Google 連携が「未接続」表示（"Connect
      Google before exporting." 通知）になっている
   3. 再度「Export to Google Sheets」を押すと
      `/ui/settings?status=google_not_connected` にリダイレクトされる（500 や
      `export_failed` ではない）
   4. API ログに `settings.google_sheets.decrypt_failed` イベント
      （`request_id` / `user_id` 付き、鍵・暗号文・平文を含まない）が記録されている
   5. ブラウザから再度 Google アカウントを接続すると新鍵で暗号化された行が
      作成され、エクスポートも成功する
4. **後片付け（任意）**
   - 元の鍵に戻したい場合は、もう一度 `migrations/005_*.sql` 相当の
     `DELETE FROM user_google_sheets_connections;` を実行してから再認可してください
     （旧鍵の暗号文が残っていると、復号失敗による「未接続相当」が継続します）

### 失敗パターン早見表

| 症状 | 推定原因 | 対応 |
|---|---|---|
| API 起動時に `panic: missing env: ENCRYPTION_KEY` | 環境変数未設定 | `.env` を確認 |
| `panic: invalid env: ENCRYPTION_KEY (base64 ...)` | base64 として decode 失敗 | 値を再生成・コピペし直し |
| `panic: invalid env: ENCRYPTION_KEY (must be 32 bytes...)` | デコード後 32 バイトでない | `openssl rand -base64 32` で生成 |
| `/ui/settings` が常に「未接続」 | 既存行を別鍵で復号している | 新鍵で再認可 or DELETE |
| ログに `request_id` 以外の情報が出ている | 復号エラー処理の regression | コードレビューで `slog.Warn` 引数を確認 |
