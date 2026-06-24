# Implementation Notes (#119)

## Implementation Notes

### Task 1
- 採用方針: tasks.md task 1 の指示通り、`migrations/007_add_item_status.sql` を新規作成し、`ALTER TABLE items ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unread'` + `DO $$ EXCEPTION` ブロック包みの CHECK 制約 + `CREATE INDEX IF NOT EXISTS items_user_status_idx` の 3 ステートメント構成で実装した。
- 重要な判断:
  - backfill は ADD COLUMN ... NOT NULL DEFAULT 'unread' により自動成立するため、追加 UPDATE 文は導入しなかった（design.md "Migration Strategy" / Req 1.3 / 6.1 と整合。PostgreSQL 11+ の高速 default 最適化で既存行を書き換えずに NOT NULL を成立させる）。ファイル内コメントにこの判断を明記した。
  - CHECK 制約は task 1 の説明にある 2 案（`DO $$ EXCEPTION WHEN duplicate_object` / `pg_constraint` を `IF NOT EXISTS` で先読み）のうち、書式が短く 006 の冪等性パターンと整合する前者（`DO $$ BEGIN ... EXCEPTION WHEN duplicate_object THEN NULL; END $$;`）を採用した。
  - ファイル冒頭コメントは 006_item_tag_display_name.sql と同程度の粒度で「背景 / 適用順 / 冪等性パターン / backfill 戦略」の 4 ブロック構造に整理し、Issue #119 / design.md の参照点を明示した。
- 残存課題: なし（task 2 以降の store 層・server 層・MCP 層・SSR 実装が後続 task として残っているが、本 task の境界（migrations）には影響しない）。

## Verify 実行記録

- `go test ./...`: 全 package pass（cached + `internal/server` 4.012s）。SQL のみの追加のため Go コード側に影響無し。
- `golangci-lint run`: 本 per-task Implementer 実行環境に `golangci-lint` バイナリが未 install のため実行できず（`command not found`）。本 task は Go コード変更を伴わない SQL ファイル単独追加であり、lint 対象に新規 Go ファイルが含まれないため、既存 Go コードの lint 結果は変化しない（影響ゼロ）。後続 task で Go コードを追加する際に同 verify gate で lint が再実行されるため、本 task の commit 時点では問題なし。
- 実 DB への migration 適用は per-task Implementer 環境（DB を spin-up しない方針 / `.kiro/steering/structure.md` 準拠）では行わない。動作検証は task 3 の integration test（`TestMigration007_BackfillsExistingItemsToUnread`）と Reviewer フェーズの手動確認に委ねる。

## 受入基準カバレッジ（task 1 範囲）

本 task は migrations 境界の単独実装であり、対応する AC のテスト追加は task 3
（`internal/store/store_item_status_test.go` の `TestMigration007_BackfillsExistingItemsToUnread` /
`TestCreateItem_DefaultsToUnread` / `TestUpdateItemStatus_RejectsInvalidStatus`）でカバーされる
設計（tasks.md の `_Depends: 1, 2_` 規約）。task 1 単独では SQL ファイル追加のみで挙動変更は
DB 適用時にのみ顕在化するため、`_Requirements:_` 列挙 AC（1.1 / 1.2 / 1.3 / 1.5 / 6.1 / NFR 1.1 / NFR 1.2）の
対応テストは後続 task 3 / task 9（performance verification）で完結する（task 1 内では
**migration ファイルの存在と書式の正しさ自体が AC を成立させる前提条件**であり、test fixture
の追加なしに SQL ステートメントの構造をもって AC を満たす設計）。

| AC | 担保方法 | 後続 task |
|----|----------|-----------|
| Req 1.1（3 値状態を保持） | CHECK 制約 `items_status_check` で 3 値以外を DB レイヤで拒否 | task 3 `TestUpdateItemStatus_RejectsInvalidStatus` |
| Req 1.2（初期状態 unread） | `DEFAULT 'unread'` カラム定義 | task 3 `TestCreateItem_DefaultsToUnread` |
| Req 1.3（既存アイテム backfill） | `ADD COLUMN NOT NULL DEFAULT 'unread'` による自動 backfill | task 3 `TestMigration007_BackfillsExistingItemsToUnread` |
| Req 1.5（範囲外を拒否） | CHECK 制約 `items_status_check` | task 3 `TestUpdateItemStatus_RejectsInvalidStatus`、server task 4 `TestHandleSetItemStatusInvalidStatusReturns400` |
| Req 6.1（データ消失なし） | `ADD COLUMN NOT NULL DEFAULT 'unread'` の自動 backfill | task 3 `TestMigration007_BackfillsExistingItemsToUnread` |
| NFR 1.1（一覧表示パフォーマンス） | 複合 index `items_user_status_idx (user_id, status, created_at DESC)` | task 9 / Verify 節 "Performance verification" |
| NFR 1.2（タブ切替パフォーマンス） | 同上の複合 index | task 9 / Verify 節 "Performance verification" |

STATUS: complete
