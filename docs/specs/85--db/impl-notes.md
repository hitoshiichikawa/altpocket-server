# Implementation Notes — Issue #85: ヘルスチェックで DB 接続を確認する

## 実装サマリ

- `internal/server/health.go` (新規) — `/healthz` (liveness) と `/readyz`
  (readiness) のハンドラ。`pinger` インターフェースを切り出し、DB ping を
  2 秒タイムアウトで実施。失敗 / タイムアウト時は `503 unavailable` を返し、
  構造化 WARN ログを出す。
- `internal/server/server.go` — `Routes()` に `/readyz` ルートを追加
  (line 126)。`handleHealth` を `health.go` へ移設し、`Server` 構造体に
  テスト差し替え用の `readyPingerFn func(context.Context) error` を追加。
- `internal/server/health_test.go` (新規) — `/healthz` の DB 非依存性、
  `/readyz` の成功 / 失敗 / タイムアウト / pinger 未設定 / 機密漏えい防止を
  カバーする単体テスト。

## 設計上の判断

### `pinger` インターフェース導入

要件には書かれていないが、`/readyz` の失敗系・タイムアウト系を実 DB なしで
検証可能にするため、`pinger interface { Ping(ctx context.Context) error }`
という最小の局所インターフェースを `health.go` 内に定義し、`Server` に
`readyPingerFn` (関数フィールド) を追加した。

- 本番は `s.store.DB` (`*pgxpool.Pool`) がそのまま `pinger` を満たすため、
  既存呼び出し経路の挙動・パフォーマンス・所有関係は一切変えない。
- テストは `readyPingerFn` を設定するだけで成功 / 失敗 / 永久ブロック
  (タイムアウト試験) のいずれもインメモリで再現できる。
- インターフェースは `health.go` の package private にしているため、外部
  パッケージから依存される心配がなく、将来の差し替え自由度を保つ。

### `/readyz` 失敗時のログ vs レスポンスの分離

要件 2.6 は「レスポンス body **または**ログのいずれかに失敗事実が残ること」
を求めている。両方に出すと NFR 2.2 (内部スタックトレース・SQL 文・ホスト名等
の漏えい禁止) のリスクが上がるため、

- **レスポンス**: 固定文字列 `"unavailable"` のみ。reason / err は含めない。
- **ログ (WARN)**: `event=health.ready.unavailable, request_id=..., reason=...,
  error=<driver msg>` を構造化出力。

の分離を採用。これにより前段ロードバランサは 503 を機械判定でき、運用者は
ログから失敗原因を確認できる。

### pinger 未設定時の挙動 (Requirement 2.3 / 4.4)

`s.store` または `s.store.DB` が nil の場合 (たとえばテスト server や、
将来の構成変更で DB を later-bind するケース)、`/readyz` は panic せず
503 を返す。理由:

- 要件 2 系は「DB ping できない状態 = readiness NG」を表現すべきと読める。
- ハンドラ層での panic は CLAUDE.md 禁止事項。
- 503 を返すことで前段ロードバランサがインスタンスを切り離せる。

### 既存 `handleHealth` の挙動温存

`/healthz` のレスポンス (HTTP 200 + body `"ok"`、Content-Type 未指定 = Go
標準の `text/plain; charset=utf-8` 自動付与) は本要件導入前と完全に同一。
スモークテスト (`scripts/test-api.sh`)・本番デプロイドキュメント
(`docs/production-docker-deploy*.md`)・`docs/local-docker-deploy.md` の
記述は変更不要。

## 検証結果

| コマンド | 結果 |
|---|---|
| `go test ./...` | **pass** (全パッケージ ok、altpocket/internal/server 2.010s) |
| `go vet ./...` | **pass** (出力なし) |
| `go build ./...` | **pass** (出力なし) |
| `gofmt -l health.go health_test.go` | **clean** (私の追加ファイルは違反なし) |
| `golangci-lint run` | **未実行** (環境に未インストール)。`.golangci.yml` 有効 linter は govet / errcheck / staticcheck。`go vet` は pass。errcheck は `_, _ = w.Write(...)` で抑制済み。staticcheck の影響は不明だが、コード自体は単純で明らかな違反なし。 |

新規テストの内訳:

| テスト | 対応 AC |
|---|---|
| `TestHandleHealthAlwaysReturnsOK` (3 サブテスト) | 1.1, 1.2, 1.3, 1.4, 5.4 |
| `TestHandleReadySuccess` | 2.1, 2.2, 5.1 |
| `TestHandleReadyFailureReturns503` (3 サブテスト) | 2.3, 5.2 |
| `TestHandleReadyTimeoutReturns503` | 2.4, 2.5, 5.3, NFR 1.2 |
| `TestHandleReadyPingerUnavailableReturns503` | 2.3, 4.4 |
| `TestHandleReadyResponseBodyContainsNoSecrets` | 2.7, NFR 2.2 |

既存テスト regression なし (`extension_contract_test.go` 含む全 pass)。

### Requirement 別 トレーサビリティ

| Req ID | カバー方法 |
|---|---|
| 1.1 | `TestHandleHealthAlwaysReturnsOK/pinger_would_succeed` |
| 1.2 | `TestHandleHealthAlwaysReturnsOK` (全 3 ケースで `pingCalls=0` を検証) |
| 1.3 | `TestHandleHealthAlwaysReturnsOK/pinger_would_fail_if_invoked` |
| 1.4 | `TestHandleHealthAlwaysReturnsOK` (body=="ok", status==200 を検証。Content-Type は変更なし) |
| 2.1 | `TestHandleReadySuccess` (`pingCalls==1` を検証) |
| 2.2 | `TestHandleReadySuccess` (200 + "ok") |
| 2.3 | `TestHandleReadyFailureReturns503` + `TestHandleReadyPingerUnavailableReturns503` (503) |
| 2.4 | `TestHandleReadyTimeoutReturns503` (503) |
| 2.5 | `TestHandleReadyTimeoutReturns503` (elapsed < 4s を検証、定数 `readyDBPingTimeout=2*time.Second`) |
| 2.6 | `health.go:logReadyFailure` で構造化 WARN を emit (テストはログ出力検証なし。ログ I/O は io.Discard でスモーク。実装目視確認) |
| 2.7 | `TestHandleReadyResponseBodyContainsNoSecrets` |
| 3.1 | server.go ルート登録は `r.Get("/healthz", ...)` 直下 (auth middleware 経由しない) |
| 3.2 | server.go ルート登録は `r.Get("/readyz", ...)` 直下 (auth middleware 経由しない) |
| 3.3 | ハンドラ実装は session/JWT/API Key を一切読まない (実装目視) |
| 4.1 | 既存ルートは `git diff` で path/method 不変。`go test ./...` 全 pass |
| 4.2 | `/readyz` は既存ルートと衝突しない (新規 path) |
| 4.3 | `r.Get(...)` で両エンドポイント登録 |
| 4.4 | `go test ./...` 全 pass |
| 4.5 | `scripts/test-api.sh` は line 163-165 で `GET /healthz` が 200 + "ok" を期待。私の変更で /healthz の応答は不変なのでスクリプトは継続成功する (手動実行は本ステージ対象外) |
| 5.1 | `TestHandleReadySuccess` |
| 5.2 | `TestHandleReadyFailureReturns503` |
| 5.3 | `TestHandleReadyTimeoutReturns503` |
| 5.4 | `TestHandleHealthAlwaysReturnsOK` |
| NFR 1.1 | /healthz は無条件で `WriteHeader+Write([]byte("ok"))` のみ。DB ping せず。50ms 未満は自明 |
| NFR 1.2 | DB ping のタイムアウト 2 秒 (`readyDBPingTimeout`)、正常時はラウンドトリップ 1 回 |
| NFR 1.3 | /readyz の処理は単一 goroutine 内で `context.WithTimeout` を使い、他ハンドラに影響しない |
| NFR 2.1 | レスポンス body は固定文字列、ログには認証情報を含めない (request_id/reason/err.Error のみ) |
| NFR 2.2 | `TestHandleReadyResponseBodyContainsNoSecrets` |
| NFR 3.1 | /healthz の挙動は不変なので production-docker-deploy ドキュメントは変更不要 |
| NFR 3.2 | /healthz の応答は不変なので scripts/test-api.sh / scripts/test-api.ps1 の期待値は変更不要 |

## 確認事項

なし。要件は明確で、設計・実装上の解釈で迷う点はなかった。

`golangci-lint` がローカル環境に未インストールだったため、CI 通過は GitHub
Actions 側に委ねる。`go vet` 通過と `.golangci.yml` の enabled linter
(govet / errcheck / staticcheck) を踏まえると問題ない見通し。

STATUS: complete
