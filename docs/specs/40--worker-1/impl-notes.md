# Implementation Notes — Issue #40 Worker startup immediate cycle

## 実装サマリ

- `cmd/worker/main.go` の `for { select { ... } }` 突入直前に、定期ループ本体と
  同一順序（`cleanupSessions` → `cleanupRefreshTokens` → `runOnce`）で 1 サイクル
  分の処理を 1 回実行する処理を追加した。
- 上記 3 行のロジックは `runStartupCycle(sessionFn, refreshFn, fetchFn func())`
  という小さな純粋関数に切り出し、`*store.Store` / `*fetcher.Fetcher` / 実 DB
  への依存無しで単体テストできる形にした。`main` からはそれぞれ
  `func() { cleanupSessions(ctx, st, log) }` 等のクロージャを渡している。
- 既存の `cleanupSessions` / `cleanupRefreshTokens` / `runOnce` のシグネチャ・
  内部ロジック・ログキー・エラーハンドリング方針（log-and-return、panic させ
  ない）は一切変更していない。要件 4.x（エラー耐性）と NFR 1.1（ログ仕様）は
  これら既存関数の挙動の再利用で満たしている。
- `signal.Notify` は startup cycle 実行前に登録済みのため、1 サイクル中に
  SIGINT/SIGTERM が来た場合は startup cycle 完了後の最初の `select` で
  `<-done` ケースに入って `worker_shutdown` を出して終了する（要件 5.3 を満
  たす。実行中の処理は既存と同じく `context.WithTimeout(ctx, 12*time.Second)`
  で打ち切られる）。
- ticker 間隔（1 分）、`fetcher.New(1_000_000, fullLimit, ContentSearchLimit)`
  の引数、`sem` のサイズ（10）、フェッチタイムアウト（12 秒）、バッチサイズ
  （50）、`config` ロード仕様、終了コード仕様は変更していない（NFR 2.1 / Out
  of Scope）。

## テスト方針

DB / 外部依存なしで「起動直後 1 サイクルロジック」だけを検証するため、
`runStartupCycle` を 3 つの `func()` 引数で受ける純粋関数として切り出し、
`cmd/worker/main_test.go` に以下を追加した。

| テスト名 | 観点 | 担保する AC |
|---|---|---|
| `TestRunStartupCycleInvokesAllOnce` | session/refresh/fetch がそれぞれ 1 回ずつ呼ばれる | Req 1.1 / 1.2 / 1.3 |
| `TestRunStartupCycleOrderMatchesTickerLoop` | 呼び出し順が `session → refresh → fetch` で固定 | Req 1.5 |
| `TestRunStartupCycleContinuesAfterStepReturn` | 各 step が return しても後続 step が必ず実行される（短絡しない） | Req 4.1 / 4.2 / 4.3 の前提条件（既存 step の log-and-return をブロックしない） |
| 既存 `TestClassifyFetchErrorNoContent` / `TestClassifyFetchErrorUnknown` | 退行確認 | NFR 2.2 |

Red → Green を確認済み（`runStartupCycle` 未定義状態でテストがビルドエラー
となることを `go test` 出力で確認 → 実装後 5 件全 PASS）。

## 受入基準カバレッジ

| AC | 担保方法 |
|---|---|
| 1.1 起動直後にコンテンツ取得 1 回 | `runStartupCycle` 内の `fetchFn()` 呼び出し、`TestRunStartupCycleInvokesAllOnce` |
| 1.2 起動直後にセッション削除 1 回 | `runStartupCycle` 内の `sessionFn()` 呼び出し、`TestRunStartupCycleInvokesAllOnce` |
| 1.3 起動直後にリフレッシュトークン削除 1 回 | `runStartupCycle` 内の `refreshFn()` 呼び出し、`TestRunStartupCycleInvokesAllOnce` |
| 1.4 起動直後 1 サイクルのログ仕様が定期実行と同一 | 既存 `cleanupSessions` / `cleanupRefreshTokens` / `runOnce` をそのまま呼び直すクロージャを渡しているため、ログキー・フィールドは定期実行と同一。コードリーディング担保（実 DB が必要なため単体テストでは検証していない） |
| 1.5 起動直後 3 処理の実行順 = 定期実行順 | `runStartupCycle` の引数順と main 側ループの順序を一致させた `cleanupSessions → cleanupRefreshTokens → runOnce`。`TestRunStartupCycleOrderMatchesTickerLoop` |
| 2.1 起動前後に投入されたアイテムが起動後最初のサイクルで拾われる | 最初の処理が `runOnce` を含むため、`ClaimItemsForFetch(ctx, 50)` が最初のサイクルでバッチ取得する。コードリーディング担保（実 DB 必要） |
| 2.2 最初のサイクル開始までの待機が ticker 間隔（1 分）に依存しない | `runStartupCycle` は `signal.Notify` 直後・`for` ループ突入前に同期実行される。コードリーディング担保 |
| 3.1 1 サイクル後も 1 分間隔で継続 | `for { select { case <-ticker.C: ... } }` を変更していない（diff で確認可能） |
| 3.2 ticker 側の頻度・順序・引数を変更しない | 既存ループ本体は無変更。diff 確認 |
| 3.3 起動直後実行と最初の ticker 発火の重複・競合時も単位ごとに完了 | 各 step は同期呼び出しで完了後に return。`runOnce` 内の goroutine は `wg.Wait()` で待つため、3 step の境界で必ず収束する。重複しても各 step の単位（DB 1 トランザクション・1 アイテム）は独立 |
| 4.1 fetch 失敗時にプロセス継続 | 既存 `runOnce` の log-and-return（`worker_claim_failed` / `worker_fetch_failed`）を温存。`TestRunStartupCycleContinuesAfterStepReturn` で短絡しないことを担保 |
| 4.2 セッション削除失敗時にプロセス継続 | 既存 `cleanupSessions` の log-and-return を温存。同上テスト |
| 4.3 リフレッシュトークン削除失敗時にプロセス継続 | 既存 `cleanupRefreshTokens` の log-and-return を温存。同上テスト |
| 4.4 panic させない | 既存 3 関数いずれも panic 経路無し。`runStartupCycle` 自身も recover 不要な単純呼び出しのみ |
| 5.1 SIGINT で `worker_shutdown` ログ出力して終了 | `signal.Notify` を `runStartupCycle` 実行前に登録、`for { select }` 内 `<-done` 経路を変更していない |
| 5.2 SIGTERM で同上 | 同上 |
| 5.3 1 サイクル中のシグナル受信で不整合なく終了 | startup cycle は同期実行で `wg.Wait()` まで含む。完了後の最初の select で `<-done` を拾う。`runOnce` 内のフェッチは `context.WithTimeout(ctx, 12*time.Second)` で打ち切られる |
| 5.4 シグナル受信から終了までの所要時間が 1 サイクル相当を超えない | startup cycle は最大でも `12s × バッチ` のオーダーで完了し、その後 `<-done` を即座に拾う。延長要素なし |
| NFR 1.1 起動直後 1 サイクルでも既存ログを同一フィールドで出力 | 既存 3 関数を直接呼び出しているため自動的に同一 |
| NFR 1.2 機密情報をログに出さない | 既存ログ出力に新規フィールド追加なし |
| NFR 2.1 起動方法・環境変数・終了コード仕様を変更しない | `main` の前段（config / DB / fetcher 初期化）と `os.Exit(1)` 経路は無変更 |
| NFR 2.2 既存テスト・lint 通過状態を維持 | `go test ./...` 全パス（下記実行結果参照） |

## 実行結果

- `go test ./...` → 全パッケージ ok（cmd/worker は `TestClassifyFetchErrorNoContent`、
  `TestClassifyFetchErrorUnknown`、`TestRunStartupCycleInvokesAllOnce`、
  `TestRunStartupCycleOrderMatchesTickerLoop`、`TestRunStartupCycleContinuesAfterStepReturn`
  の 5 件 PASS）
- `golangci-lint run` → このワークツリー環境に `golangci-lint` バイナリが
  未インストールのため未実施。代替として `go vet ./...` を実行しエラー無しを
  確認した。CI（`.github/workflows/ci.yml`）側で `golangci-lint` v2.11.4 が走る
  ため、最終的な lint 担保はそちらで行う想定。
- `go build ./...` → エラーなし

## 確認事項（PR 本文に転記想定）

1. **`runStartupCycle` の関数引数化**: 「直線的に書く」原則に対し抽象度を 1 段
   上げる選択をしている。代案として「`main` 内に 3 行直書きしてテストを書か
   ない」もあったが、要件 1.1〜1.5 を AC レベルで担保するために最小抽象として
   採用した。妥当か確認願いたい。
2. **既存 `cleanupSessions` / `cleanupRefreshTokens` / `runOnce` のシグネチャ
   は無変更**とした。要件 4.x（エラー耐性）は既存実装の log-and-return が満た
   していると判断している。仕様書の Out of Scope と整合。
3. **重複・競合（要件 3.3）**: startup cycle 完了直後に ticker が 1 分後に発火
   する設計になっており、現実装上「実行重複」は時間的に発生しない（1 ticker
   サイクル分以上は startup cycle が走り終わっている）。要件 3.3 は安全側の
   要件として読み、startup と ticker の直接的な重複防止ロジック（mutex 等）は
   追加していない。仕様意図と一致しているか念のため確認。
4. **環境ローカル lint 実行不可**: 上記のとおり `golangci-lint` がこのワーク
   ツリー環境に存在しないため、ローカルでは `go vet` のみ実施。CI で本物の
   `golangci-lint` が走る前提。

## 派生タスク候補

- なし（Issue #102 で扱う ticker 間隔短縮 / LISTEN-NOTIFY 化はそちらに集約済み）。
