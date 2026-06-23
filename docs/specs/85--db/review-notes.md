# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-06-23T00:00:00Z -->

## Reviewed Scope

- Branch: claude/issue-85-impl--db
- HEAD commit: 0778e9f9f614866ad260e03b2125910cf782e551
- Compared to: develop..HEAD
- 構成: design-less impl（`tasks.md` / `design.md` 不在のため、境界判定は対象 repo の CLAUDE.md
  「禁止事項」「コード規約」と requirements.md に従う）

## Verified Requirements

- 1.1 — `internal/server/health.go:handleHealth` が `WriteHeader(200) + Write("ok")` を無条件で実行。
  `TestHandleHealthAlwaysReturnsOK/pinger_would_succeed` で検証
- 1.2 — handleHealth は DB ping を呼ばない。`TestHandleHealthAlwaysReturnsOK` の全 3 サブテストで
  `pingCalls == 0` をアサート
- 1.3 — `TestHandleHealthAlwaysReturnsOK/pinger_would_fail_if_invoked`（ping が呼ばれていれば
  失敗するが pingCalls=0 なので呼ばれず、200 + "ok" が返る）で検証
- 1.4 — `internal/server/server.go` の diff 上、`handleHealth` は旧実装と同一バイト列を出力
  （`WriteHeader(200)` + `Write([]byte("ok"))`。Content-Type も明示設定しないため Go 標準の
  自動付与挙動を維持）
- 2.1 — `handleReady` は `p.Ping(ctx)` を 1 回だけ呼ぶ。`TestHandleReadySuccess` で
  `pingCalls == 1` を検証
- 2.2 — `TestHandleReadySuccess` で 200 + body "ok" を検証
- 2.3 — `TestHandleReadyFailureReturns503`（3 サブテスト: connection refused / auth rejected /
  generic db error）と `TestHandleReadyPingerUnavailableReturns503` で 503 を検証
- 2.4 — `TestHandleReadyTimeoutReturns503` で fake pinger を `<-ctx.Done()` で block させ、
  ハンドラ側 `context.WithTimeout` 経由で 503 が返ることを検証
- 2.5 — `readyDBPingTimeout = 2 * time.Second` 定数（`health.go:14`）。
  `TestHandleReadyTimeoutReturns503` で経過時間 < 4s を確認し、5s で test guard を発火
- 2.6 — `health.go:logReadyFailure` が `slog.Warn("health.ready.unavailable", request_id, reason, error)`
  を構造化ログとして emit。AC は「body または log のいずれか」なので log 経路で満たす
- 2.7 — `TestHandleReadyResponseBodyContainsNoSecrets` で sensitive DSN fragment
  （supersecretpw / postgres:// / db.internal / alice）が body に含まれないことを検証。
  body は固定文字列 "unavailable" のみ
- 3.1 — `server.go:125` で `/healthz` を root に直接登録、`/v1` 以下の auth middleware の外
- 3.2 — `server.go:126` で `/readyz` を root に直接登録、auth middleware の外
- 3.3 — `handleHealth` / `handleReady` 実装は session/JWT/API Key を一切読まない（diff 目視確認）。
  ログ出力も request_id / reason / error.Error のみで認証情報を含まない
- 4.1 — diff 確認: 既存ルート（`/`, `/register`, `/v1/*`, `/ui/*`, `/mcp/*`, `/manifest.webmanifest`,
  `/sw.js`, `/static/*`）の path・method・ハンドラ割当は不変
- 4.2 — `/readyz` は新規 path で既存ルートと衝突しない
- 4.3 — `r.Get("/healthz", ...)` / `r.Get("/readyz", ...)` の双方が GET 登録
- 4.4 — impl-notes に `go test ./...` pass 記録（altpocket/internal/server 2.010s）。新規 test も
  既存 test helper（`newAuthTestServer`）を流用するため regression なし
- 4.5 — `/healthz` の応答が完全に不変なため `scripts/test-api.sh` line 163-165 の期待値
  （200 + "ok"）は変更不要
- 5.1 — `TestHandleReadySuccess`（200 系テストの構造が成立）
- 5.2 — `TestHandleReadyFailureReturns503`（503 系テストの構造が成立）
- 5.3 — `TestHandleReadyTimeoutReturns503`（タイムアウト系テストの構造が成立。`pinger` 抽象により
  実 DB なしで再現可能）
- 5.4 — `TestHandleHealthAlwaysReturnsOK`（/healthz の DB 非依存性テストの構造が成立）
- NFR 1.1 — `handleHealth` は無条件 200 + "ok" のみで I/O ゼロ、50ms 未満は自明
- NFR 1.2 — `readyDBPingTimeout = 2s` で異常時上限を保証、正常時は ping 1 回のみ
- NFR 1.3 — `context.WithTimeout(r.Context(), 2s)` が当該 goroutine 内で完結し他ハンドラを
  ブロックしない（Go の標準 net/http が per-request goroutine 起動するため）
- NFR 2.1 — body は固定文字列、log フィールドは request_id / reason / err.Error のみ
- NFR 2.2 — `TestHandleReadyResponseBodyContainsNoSecrets` で defense-in-depth として検証
- NFR 3.1 — `/healthz` の応答が不変なため `docs/production-docker-deploy*.md` /
  `docs/local-docker-deploy.md` の記述変更不要
- NFR 3.2 — `/healthz` の応答が不変なため `scripts/test-api.sh` / `scripts/test-api.ps1` の期待値
  変更不要

## Boundary 検証

- design-less impl のため `tasks.md` の `_Boundary:_` 制約は不在。CLAUDE.md 観点で確認:
  - HTTP ハンドラは `internal/server` 配下に集約 ✅
  - DB 操作は `internal/store.Store.DB` (pgxpool.Pool) 経由で接続のみ取得し、`pinger` 局所 interface
    で抽象化（package private のため外部依存なし） ✅
  - ハンドラ層で `panic` していない（pinger 未設定でも 503 を返す） ✅
  - `slog` JSON で構造化ログを出力、トークン・Cookie・OAuth 生レスポンスを含まない ✅
  - extension API（`extension_contract_test.go`）に影響する変更なし ✅
  - migrations の追加なし ✅
  - 環境変数追加なし（タイムアウト値は定数固定） ✅

## Findings

なし

## Summary

全 numeric AC（Req 1.1–4.5, 5.1–5.4 + NFR 1.1–3.2）について、`health.go` / `health_test.go` /
`server.go` の差分と既存 helper を突き合わせて実装・テストいずれかでカバーされていることを
確認した。design-less impl だが CLAUDE.md の禁止事項・コード規約に違反する点はなく、
`go test ./...` も pass 済み。境界逸脱・AC 未カバー・missing test のいずれにも該当しない。

RESULT: approve
