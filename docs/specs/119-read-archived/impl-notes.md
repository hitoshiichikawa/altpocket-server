# Implementation Notes (Issue #119: read / archived 状態の導入)

## Implementation Notes

### Task 1

- 採用方針: `IF NOT EXISTS` (column / index) + `DO $$ EXCEPTION WHEN duplicate_object $$` (CHECK) で idempotent な forward-only migration を構成した
- 重要な判断: PostgreSQL 16 に `ADD CONSTRAINT IF NOT EXISTS` が無いため CHECK は plpgsql ブロックで包んだ。`DEFAULT 'unread'` を `NOT NULL` 列に付けることで既存全行を 1 ステートメントで backfill し、Req 1.3 / 6.1 を追加 UPDATE なしで満たす。`items_user_status_idx` は `(user_id, status, created_at DESC)` の compound とし、既存 `items_user_created_idx` は worker / 全件取得経路のため温存
- 残存課題: なし（後続 task 2 で Go 側 `Item.Status` 追加・store 拡張に進む）
