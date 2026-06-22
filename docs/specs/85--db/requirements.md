# Requirements Document

## Introduction

altpocket API の `/healthz` は現在、DB 接続状態に関係なく常に HTTP 200 と body `"ok"` を
返している。このため DB がダウンしていてもロードバランサや orchestrator から見れば
インスタンスは健全と判定され、トラフィックが流入し続けて利用者にエラーが返る状態が
継続する。本要件では Kubernetes 等で慣習化された liveness / readiness の分離方針
（Issue #85 Triage で Option B として確定）を採用し、`/healthz` を軽量な liveness として
温存しつつ、新規エンドポイント `/readyz` で DB 接続性を検査して 503 を返せるようにする。
既存の `/healthz` 利用箇所（スモークテスト・本番デプロイドキュメント）の挙動を一切
変更しないことが本要件の前提である。

参照: [Issue #85](https://github.com/hitoshiichikawa/altpocket-server/issues/85)

## Requirements

### Requirement 1: Liveness エンドポイント `/healthz` の後方互換維持

**Objective:** As altpocket をセルフホストで運用する管理者, I want `/healthz` がプロセス
稼働の有無のみを示す軽量な liveness 応答を返し続けること, so that 既存のデプロイ
ドキュメント・スモークテスト・前段のロードバランサ設定を一切変更せずに済む

#### Acceptance Criteria

1. When API プロセスが起動して HTTP リクエストを処理可能な状態にある, the API shall
   `GET /healthz` に対して HTTP 200 と body `"ok"` を返す
2. When `GET /healthz` を処理する, the API shall DB への接続確認（DB ping）を行わない
3. When DB がダウンしている状態で `GET /healthz` を受信した, the API shall それでも
   HTTP 200 と body `"ok"` を返す
4. The API shall `/healthz` のレスポンスステータス・body 文字列・Content-Type 既定値を
   本要件導入前と同一に保つ

### Requirement 2: Readiness エンドポイント `/readyz` の新設と DB 接続確認

**Objective:** As altpocket をセルフホストで運用する管理者, I want `/readyz` が DB 接続性を
含む「実際にリクエストを処理できる状態か」を返すこと, so that DB ダウン時に前段の
ロードバランサ／orchestrator がトラフィックを切り離せる

#### Acceptance Criteria

1. When `GET /readyz` を受信した, the API shall リクエスト処理の一部として DB への ping を
   1 回実施する
2. When DB ping が成功した, the API shall HTTP 200 と body `"ok"` を返す
3. If DB ping が失敗した（接続不可・認証拒否・DB 側エラー応答等を含む）, the API shall
   HTTP 503 を返す
4. If DB ping がタイムアウトした, the API shall HTTP 503 を返す
5. The API shall `/readyz` の DB ping に 2 秒のタイムアウトを設定する
6. When DB ping が 503 で失敗した, the API shall レスポンス body または構造化ログのいずれかに
   失敗事実が運用者から識別できる情報を残す
7. The API shall `/readyz` のレスポンスに DB 接続文字列・パスワード・内部スタックトレースを
   含めない

### Requirement 3: 認証・公開性

**Objective:** As altpocket をセルフホストで運用する管理者, I want `/healthz` と `/readyz` が
認証なしで叩けること, so that 前段のロードバランサや orchestrator のヘルスプローブが
追加の認証情報なしに動作する

#### Acceptance Criteria

1. The API shall `/healthz` を認証なしで応答可能にする
2. The API shall `/readyz` を認証なしで応答可能にする
3. When `/healthz` または `/readyz` を処理する, the API shall セッション Cookie・JWT・API Key
   などの認証情報をレスポンス body・ヘッダ・ログに出力しない

### Requirement 4: ルーティングと既存挙動の非破壊

**Objective:** As altpocket を継続開発する開発者, I want 既存のルーティング・既存テスト・
既存ハンドラ群が本要件導入で破壊されないこと, so that 1 PR 1 Issue の原則を保ったまま
安全に変更を取り込める

#### Acceptance Criteria

1. The API shall 既存ルート（`/`, `/register`, `/v1/*`, `/ui/*`, `/mcp/*`,
   `/manifest.webmanifest`, `/sw.js`, `/static/*`）の path・method・レスポンスを本要件
   導入前と同一に保つ
2. When `GET /readyz` を受信した, the API shall いずれの既存ルートとも path 衝突しない
3. The API shall `/healthz` と `/readyz` の双方を `GET` メソッドで応答可能にする
4. When 既存の Go テストスイート（`go test ./...`）を実行する, the test suite shall 本要件
   導入前と同一に成功する
5. When スモークテストスクリプト（`scripts/test-api.sh`）を実行する, the script shall 本要件
   導入前と同一に成功する

### Requirement 5: 検証可能性

**Objective:** As 本機能を実装・回帰テストする開発者, I want `/readyz` の正常系・異常系・
タイムアウトが自動テストで検証できること, so that DB 障害時の挙動を CI で継続的に
回帰検出できる

#### Acceptance Criteria

1. The API shall `/readyz` の DB ping 正常系（HTTP 200）を検証するテストを追加できる構造を
   持つ
2. The API shall `/readyz` の DB ping 失敗系（HTTP 503）を検証するテストを追加できる構造を
   持つ
3. The API shall `/readyz` の DB ping タイムアウト系（HTTP 503）を検証するテストを追加できる
   構造を持つ
4. The API shall `/healthz` が DB の状態に依存せず HTTP 200 を返すことを検証するテストを
   追加できる構造を持つ

## Non-Functional Requirements

### NFR 1: 性能・可用性

1. The API shall `/healthz` の応答時間を DB 状態に関わらず 50ms 未満（同一プロセス内処理の
   時間）に保つ
2. The API shall `/readyz` の応答時間を DB 正常時に通常 100ms 未満（DB ping 1 回のラウンド
   トリップを含む）に収め、異常時も Requirement 2 のタイムアウト（2 秒）を超えない
3. The API shall `/readyz` の DB ping 失敗・タイムアウトによって他のルートのリクエスト処理を
   ブロックまたは遅延させない

### NFR 2: セキュリティ

1. The API shall `/healthz` と `/readyz` のレスポンス・ログに DB 接続文字列・パスワード・
   セッション Cookie・トークン類を出力しない
2. The API shall `/readyz` の 503 応答 body に内部スタックトレース・SQL 文・ホスト名等の
   機微情報を含めない

### NFR 3: 互換性

1. The API shall 既存の本番デプロイドキュメント（`docs/production-docker-deploy.md`,
   `docs/production-docker-deploy-windows.md`, `docs/local-docker-deploy.md`）に記載された
   `/healthz` の使い方を変更不要にする
2. The API shall 既存スモークテスト（`scripts/test-api.sh`, `scripts/test-api.ps1`）の
   `/healthz` 期待値（HTTP 200 + body `"ok"`）を変更不要にする

## Out of Scope

- 外部 API（Google OAuth, Google Sheets, MCP 連携先等）の包括的なヘルスチェック
- メトリクス／監視ダッシュボード連携（Prometheus メトリクスや外形監視サービスへの export）
- Worker プロセス（`cmd/worker`）の readiness 表現
- DB スキーマ／マイグレーション適用状況のチェック
- DB レプリカ・読み取り専用ノードの個別ヘルス判定
- 既存ヘルスチェック呼出元（前段ロードバランサ・本番デプロイドキュメント）の設定変更や
  `/readyz` への切替案内
- `/healthz` または `/readyz` への認証付与・rate limit 追加

## Open Questions

なし

## 関連

- Related: #85
