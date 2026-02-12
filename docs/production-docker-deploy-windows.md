# 本番向け Docker デプロイ手順（Windows / インターネット公開）

この手順は、Windows 上で Docker を使って `db` + `api` + `worker` を起動し、`Caddy` で `https://` 公開するための手順です。

本手順は次の構成（方式1）を前提にしています。
- Web/UI ドメイン: `www.example.invalid`
- API ドメイン: `api.example.invalid`
- 実体は同じ `api` コンテナ（コード変更なし）

> 注: 本番運用は Linux サーバーの方が一般的です。ここでは Windows で運用するケース向けに手順を整理しています。

## 0. 前提
- OS: Windows 11 Pro 以降（または Windows Server + Docker Desktop/Engine）
- Docker Desktop をインストール済み（WSL2 backend 有効）
- ドメインを所有し、`www` と `api` の A レコードを公開IPへ設定済み
- ルーター/クラウドFW/Windows Defender Firewall で `80/tcp`, `443/tcp` を開放

## 1. 事前準備（Windows）
### 1.1 Docker Desktop 設定
1. Docker Desktop を起動
2. `Settings` > `General`
   - `Use the WSL 2 based engine` を ON
3. `Settings` > `Resources` > `WSL Integration`
   - 作業する WSL ディストリを ON

### 1.2 Windows Firewall 開放（管理者 PowerShell）
```powershell
New-NetFirewallRule -DisplayName "altpocket-http" -Direction Inbound -Protocol TCP -LocalPort 80 -Action Allow
New-NetFirewallRule -DisplayName "altpocket-https" -Direction Inbound -Protocol TCP -LocalPort 443 -Action Allow
```

## 2. Google OAuth 設定（GCP Console 詳細手順）
このアプリは以下 2 種類の OAuth client が必要です。
- Web クライアント（サーバーの Google ログイン用）
- Chrome Extension クライアント（拡張機能ログイン用）

### 2.1 GCP プロジェクト選択
1. ブラウザで [https://console.cloud.google.com/](https://console.cloud.google.com/) を開く
2. 画面上部のプロジェクトセレクタ（プロジェクト名のドロップダウン）をクリック
3. 対象プロジェクトを選択（なければ `NEW PROJECT` で作成）

### 2.2 OAuth 同意画面の設定（Google Auth Platform）
1. [https://console.cloud.google.com/auth/overview](https://console.cloud.google.com/auth/overview) を開く
2. 左メニュー `Branding` をクリック
3. 初回なら `Get started` をクリック
4. 以下を入力して `Save`
   - App name
   - User support email
   - Developer contact information
5. 左メニュー `Audience` をクリック
6. `User type` を `External` に設定
7. `Test users` セクションで `ADD USERS` をクリックし、検証に使うGoogleアカウントを追加

### 2.3 Web OAuth クライアント作成（サーバー用）
1. [https://console.cloud.google.com/auth/clients](https://console.cloud.google.com/auth/clients) を開く
2. `+ CREATE CLIENT` をクリック
3. `Application type` で `Web application` を選択
4. `Name` を入力（例: `altpocket-web-prod`）
5. `Authorized redirect URIs` で `+ ADD URI` をクリックし、以下を追加
   - `https://<WWWドメイン>/v1/auth/google/callback`
6. `Create` をクリック
7. 表示される `Client ID` と `Client secret` を控える

### 2.4 Chrome Extension OAuth クライアント作成
1. Chrome で `chrome://extensions` を開く
2. 右上 `Developer mode` を ON
3. `Load unpacked` をクリックし、`<repo>\extension` を読み込む
4. 読み込まれた拡張機能カードで `ID`（32文字）をコピー
5. GCP の [https://console.cloud.google.com/auth/clients](https://console.cloud.google.com/auth/clients) に戻る
6. `+ CREATE CLIENT` をクリック
7. `Application type` で `Chrome Extension` を選択
8. `Name` を入力（例: `altpocket-extension-prod`）
9. `Item ID` に手順4でコピーした拡張機能IDを貼り付け
10. `Create` をクリック
11. 表示された `Client ID` を控える

### 2.5 リポジトリ反映
1. `extension/popup.js` の `CLIENT_ID` を 2.4 で作成した Extension クライアントIDに変更
2. `deploy/.env.production` に以下を設定
   - `GOOGLE_WEB_CLIENT_ID`（2.3）
   - `GOOGLE_CLIENT_SECRET`（2.3）
   - `GOOGLE_EXT_CLIENT_ID`（2.4）

## 3. 本番 env ファイル作成（Windows PowerShell）
リポジトリルートで実行:

```powershell
Copy-Item .\deploy\.env.production.example .\deploy\.env.production
```

`deploy\.env.production` を開き、以下を必ず実値に変更:
- `WWW_HOSTNAME` / `API_HOSTNAME` / `PUBLIC_BASE_URL`
- `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`
- `DATABASE_URL`
- `SESSION_SECRET` / `JWT_SECRET`
- `GOOGLE_WEB_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` / `GOOGLE_EXT_CLIENT_ID`

シークレット生成（PowerShell）:

```powershell
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
[Convert]::ToHexString($bytes).ToLower()
```

## 4. 本番 compose ファイルをルートへコピー（PowerShell）
リポジトリルートで実行:

```powershell
Copy-Item .\deploy\docker-compose.production.yml .\docker-compose.production.yml
```

## 5. 初回デプロイ（PowerShell）
> 作業ディレクトリはリポジトリルート

### 5.1 DB 起動
```powershell
docker compose --env-file .\deploy\.env.production -f .\docker-compose.yml -f .\docker-compose.production.yml up -d db
```

### 5.2 マイグレーション
```powershell
Get-Content .\migrations\001_init.sql -Raw |
  docker compose --env-file .\deploy\.env.production -f .\docker-compose.yml -f .\docker-compose.production.yml exec -T db sh -lc 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

### 5.3 API/Worker/Edge 起動
```powershell
docker compose --env-file .\deploy\.env.production -f .\docker-compose.yml -f .\docker-compose.production.yml up -d --build api worker edge
```

## 6. 動作確認
### 6.1 APIヘルス
```powershell
curl https://<APIドメイン>/healthz
curl https://<WWWドメイン>/healthz
```
- `ok` が返ることを確認

### 6.2 API smoke test
```powershell
$env:API_BASE = "https://<APIドメイン>"
.\scripts\test-api.sh
```

### 6.3 Extension E2E
1. `https://<WWWドメイン>/v1/auth/google/login` を開き、Web 側で1回ログイン
2. `chrome://extensions` で拡張をリロード
3. 拡張 popup の `API Base URL` に `https://<APIドメイン>` を入力
4. `Login with Google` を実行
5. 任意のページで `Save Current Tab`
6. `https://<WWWドメイン>/ui/items` に保存されたことを確認

## 7. 更新デプロイ（PowerShell）
```powershell
git pull
docker compose --env-file .\deploy\.env.production -f .\docker-compose.yml -f .\docker-compose.production.yml up -d --build api worker edge
```

## 8. バックアップ（PowerShell）
```powershell
docker compose --env-file .\deploy\.env.production -f .\docker-compose.yml -f .\docker-compose.production.yml exec -T db sh -lc 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' |
  Set-Content -Encoding UTF8 ".\backup_$(Get-Date -Format yyyy-MM-dd).sql"
```

## 9. 重要な運用注意
- `deploy/.env.production` は絶対に Git へコミットしない
- `POSTGRES_PASSWORD` はローカル値の使い回し禁止
- DB ポート (`5432`) は本番で外部公開しない
- OAuth クライアントは環境（dev/staging/prod）ごとに分離する
