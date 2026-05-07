# Review Notes

<!-- idd-claude:review round=1 model=claude-opus-4-7 timestamp=2026-05-01T00:00:00Z -->

## Reviewed Scope

- Branch: claude/issue-40-impl--worker-1
- HEAD commit: 17ab70c80fa0bca812990f25de7d2caf0a8ab67d
- Compared to: main..HEAD
- Feature Flag Protocol: opt-out (CLAUDE.md `## Feature Flag Protocol` 節 `採否: opt-out`)
  → flag 観点の細目は適用しない（Req 4.2 / NFR 1.1 既定挙動）

## Verified Requirements

- 1.1 — `cmd/worker/main.go:53` で `runStartupCycle` が `runOnce` を 1 回呼び出し。
  `TestRunStartupCycleInvokesAllOnce`（`cmd/worker/main_test.go`）で 1 回呼出を検証
- 1.2 — `cmd/worker/main.go:51` で `cleanupSessions` を 1 回呼び出し。
  `TestRunStartupCycleInvokesAllOnce` で検証
- 1.3 — `cmd/worker/main.go:52` で `cleanupRefreshTokens` を 1 回呼び出し。
  `TestRunStartupCycleInvokesAllOnce` で検証
- 1.4 — 既存 `cleanupSessions` / `cleanupRefreshTokens` / `runOnce` のシグネチャ・
  ログキー（`session_cleanup` / `refresh_token_cleanup` / `worker_fetch_success`
  / `worker_fetch_failed` / `worker_claim_failed` / `worker_db_update_failed` /
  `session_cleanup_failed` / `refresh_token_cleanup_failed`）はいずれも diff で
  変更なし。startup cycle はクロージャ経由で同関数を呼ぶため自動的に同一仕様
- 1.5 — 周期ループ本体（`cmd/worker/main.go:59-61`）と startup cycle の引数順
  （`cmd/worker/main.go:50-54`）が共に session → refresh → fetch で一致。
  `TestRunStartupCycleOrderMatchesTickerLoop` で順序を検証
- 2.1 — `runOnce` 内 `ClaimItemsForFetch(ctx, 50)` が起動直後に呼ばれるため、
  起動前後に投入されたアイテムは最初のサイクルで claim 対象になる（実装の
  読み取りで担保。AC 検証としては DB が必要）
- 2.2 — `runStartupCycle` は `signal.Notify` 直後・`for` ループ突入前に同期実行
  （`cmd/worker/main.go:43-54`）。ticker 間隔に依存しない
- 3.1 — `for { select { case <-ticker.C: ... } }` ブロック（`cmd/worker/main.go:56-66`）
  は本 PR で無変更。1 分間隔の周期実行が継続
- 3.2 — diff で周期ループ本体に変更なし（順序・引数・頻度すべて温存）
- 3.3 — 各 step は同期呼出で完了し、`runOnce` 内 goroutine も `wg.Wait()` で
  収束。startup と最初の ticker 発火（約 1 分後）の時間差から実行重複は実質
  発生しない
- 4.1 — `runOnce` の log-and-return（`worker_claim_failed` / `worker_fetch_failed`）
  を温存。`runStartupCycle` は短絡しない（`TestRunStartupCycleContinuesAfterStepReturn`
  で検証）
- 4.2 — `cleanupSessions` の log-and-return（`session_cleanup_failed`）温存。同上テスト
- 4.3 — `cleanupRefreshTokens` の log-and-return（`refresh_token_cleanup_failed`）温存。同上テスト
- 4.4 — 既存 3 関数いずれも panic 経路無し。`runStartupCycle` も recover 不要な
  単純呼出のみ
- 5.1 — `signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)`（`cmd/worker/main.go:43`）
  は startup cycle 実行前に登録済み。`<-done` 経路で `worker_shutdown` ログ出力（`main.go:63`）
- 5.2 — 同上（SIGTERM）
- 5.3 — `signal.Notify` は startup 前に登録済みのためシグナル受信は失われない。
  startup cycle 完了後の最初の `select` で `<-done` を拾い `worker_shutdown` 出力。
  fetch 中は既存の `context.WithTimeout(ctx, 12*time.Second)` で打ち切られる
- 5.4 — startup cycle は最大でも 12 秒 × 並列度（`sem` サイズ 10）の範囲で完了
  し、その後即座に `<-done` を読む。延長要素なし
- NFR 1.1 — 既存 3 関数を直接呼ぶため、ログキー・フィールドは定期実行と同一
- NFR 1.2 — 新規ログフィールド追加なし。トークン・Cookie・OAuth 生レスポンスは
  既存どおりログに出力されない
- NFR 2.1 — `main` 前段の config / DB / fetcher 初期化、`os.Exit(1)` 経路、環境
  変数仕様すべて無変更
- NFR 2.2 — `go test ./cmd/worker/...` 実行で 5 件全 PASS（reviewer 自身が再実行確認）。
  `golangci-lint` は CI 側で担保

## Findings

なし

## Summary

要件 1.1〜5.4 / NFR 1.1〜2.2 のすべてに対し、実装またはテスト（または妥当な
コードリーディング担保）が確認できた。3 カテゴリ（AC 未カバー / missing test /
boundary 逸脱）のいずれにも該当しない。既存関数のシグネチャ・ログ仕様・既定値
（ticker 間隔・並列度・タイムアウト・バッチサイズ）は全て温存されており、Out
of Scope への侵入も無し。`runStartupCycle` 抽出により純粋関数として AC 1.1/1.2/1.3/1.5
を単体テストで担保している点も妥当。reviewer 環境で `go test ./cmd/worker/...`
を再実行し全 5 件 PASS を確認した。

RESULT: approve
