# Requirements Document

## Introduction

`cmd/worker` は `time.NewTicker(1 * time.Minute)` のみで処理サイクルを駆動しているため、
プロセス起動から最初の tick が発火するまで約 1 分間アイドル状態となる。Worker 起動直後に
投入された取得対象アイテムや期限切れセッション・期限切れリフレッシュトークンは、最大で
約 1 分待たされてから初めて処理される。本要件では、Worker 起動直後に 1 サイクル分の
処理（コンテンツ取得・期限切れセッション削除・期限切れリフレッシュトークン削除）が即時
実行され、その後は従来どおり 1 分間隔で処理が継続するように、起動時挙動のみを修正する。

ticker 間隔そのものの短縮や LISTEN/NOTIFY 化など、別軸の駆動方式変更は本 Issue では扱わ
ない（Issue #102 を含む別 Issue で取り扱う）。

参照: [Issue #40](https://github.com/hitoshiichikawa/altpocket-server/issues/40)

## Requirements

### Requirement 1: 起動直後の即時 1 サイクル実行

**Objective:** As a Worker プロセスを運用する運用者, I want Worker 起動直後に処理サイクル
が 1 回走ること, so that 起動直後に投入されたジョブや期限切れリソースが約 1 分間放置され
ない

#### Acceptance Criteria

1. When Worker プロセスが起動し定期処理ループに入る前, the Worker shall コンテンツ取得処
   理を 1 回実行する
2. When Worker プロセスが起動し定期処理ループに入る前, the Worker shall 期限切れセッショ
   ン削除処理を 1 回実行する
3. When Worker プロセスが起動し定期処理ループに入る前, the Worker shall 期限切れリフレッ
   シュトークン削除処理を 1 回実行する
4. When 起動直後の 1 サイクルが完了した, the Worker shall そのサイクルで処理した結果を
   従来の定期実行と同じログ仕様で出力する
5. The Worker shall 起動直後の 1 サイクルにおける 3 種の処理（セッション削除・リフレッ
   シュトークン削除・コンテンツ取得）の実行順序を、定期実行ループ内での実行順序と同一
   にする

### Requirement 2: 起動直後ジョブの遅延上限

**Objective:** As Web UI / 拡張機能経由でアイテムを保存した利用者, I want 保存直後の本文
取得が 1 分待たされないこと, so that 保存後すぐにアイテム本文を読める

#### Acceptance Criteria

1. When Worker 起動直前または起動と同時に取得対象アイテムが投入された, the Worker shall
   当該アイテムを起動後の最初の 1 サイクルで取得対象として拾う
2. The Worker shall 起動直後の最初の 1 サイクル開始までの待機時間を、定期 ticker 間隔
   （現状 1 分）に依存させず、初期化処理完了から数秒以内に開始させる

### Requirement 3: 定期サイクルの継続

**Objective:** As a Worker プロセスを運用する運用者, I want 起動直後 1 サイクル後も従来
どおり 1 分間隔で処理が回り続けること, so that 既存の運用前提（1 分粒度のクリーンアップ
頻度）が崩れない

#### Acceptance Criteria

1. When 起動直後の 1 サイクルが完了した, the Worker shall その後 1 分間隔の ticker に従っ
   てコンテンツ取得・期限切れセッション削除・期限切れリフレッシュトークン削除を継続実行
   する
2. The Worker shall 起動直後の即時実行 1 回分により、それ以降の ticker による定期実行頻
   度・実行順序・引数を変更しない
3. The Worker shall 起動直後の即時実行と最初の ticker 発火による実行とが重複・競合した場
   合でも、各処理が定期実行と同じ単位で完了するようにする

### Requirement 4: 起動直後実行のエラー耐性

**Objective:** As a Worker プロセスを運用する運用者, I want 起動直後の 1 サイクルで一時
的な障害が発生してもプロセスが継続すること, so that 一過性の DB / 外部障害で Worker が
終了してしまわない

#### Acceptance Criteria

1. If 起動直後のコンテンツ取得処理が失敗した, the Worker shall プロセスを終了させずに、
   従来の定期実行時と同じエラーログ仕様で記録した上で次の ticker サイクルに進む
2. If 起動直後の期限切れセッション削除処理が失敗した, the Worker shall プロセスを終了
   させずに、従来の定期実行時と同じエラーログ仕様で記録した上で次の処理ステップに進む
3. If 起動直後の期限切れリフレッシュトークン削除処理が失敗した, the Worker shall プロセ
   スを終了させずに、従来の定期実行時と同じエラーログ仕様で記録した上で次の処理ステップ
   に進む
4. If 起動直後の 1 サイクル中に panic が発生する状況がある, the Worker shall その状況を
   発生させない（既存の定期実行と同じく panic させない方針を起動直後実行にも適用する）

### Requirement 5: グレースフルシャットダウン互換性

**Objective:** As a Worker プロセスを運用する運用者, I want 起動直後実行を追加しても
SIGINT / SIGTERM による graceful shutdown が従来通り動作すること, so that デプロイ・再
起動時の挙動が変わらない

#### Acceptance Criteria

1. When Worker プロセスが SIGINT を受信した, the Worker shall 既存挙動どおり shutdown ロ
   グを出力して終了する
2. When Worker プロセスが SIGTERM を受信した, the Worker shall 既存挙動どおり shutdown ロ
   グを出力して終了する
3. While 起動直後の 1 サイクル実行中に SIGINT または SIGTERM を受信した, the Worker shall
   実行中の処理を不整合なく完了またはコンテキストキャンセルで打ち切ったうえで、shutdown
   ログを出力して終了する
4. The Worker shall 起動直後の 1 サイクル追加によって、シグナル受信から終了までの所要時
   間を従来の定期実行 1 サイクル相当（タイムアウト付きフェッチを含めて数十秒オーダー）
   を超えて延長しない

## Non-Functional Requirements

### NFR 1: 起動初期挙動の可観測性

1. The Worker shall 起動直後の即時 1 サイクル実行についても、定期実行と区別なく既存の
   構造化ログ（`worker_fetch_success` / `worker_fetch_failed` / `session_cleanup` /
   `refresh_token_cleanup` 等）を同一フィールドで出力する
2. The Worker shall 起動直後の即時 1 サイクルにおいてもトークン・Cookie・OAuth 生レスポ
   ンス等の機密情報をログに出力しない（既存ロギング方針を維持する）

### NFR 2: 後方互換

1. The Worker shall 既存の `cmd/worker` バイナリの起動方法・環境変数・終了コードの仕様
   を変更しない
2. The Worker shall 既存テスト（`go test ./...`）と既存 Lint（`golangci-lint run`）の通過
   状態を維持する

## Out of Scope

- ticker 間隔そのものの変更（1 分間隔は維持する）
- LISTEN/NOTIFY 等のイベント駆動化（Issue #102 で別途扱う）
- 取得対象アイテムの優先度付け・並列度（`sem` のサイズ）の変更
- 期限切れセッション・期限切れリフレッシュトークン削除のロジック変更
- 取得タイムアウト（12 秒）やバッチサイズ（50）など既存定数の変更
- メトリクス追加・外部監視連携の追加
- API サーバ（`cmd/api`）側の挙動変更

## Open Questions

- なし
