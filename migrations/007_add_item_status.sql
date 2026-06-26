-- items にユーザー可視な 3 値状態 (status) を追加し、既存行を 'unread' で backfill する。
--
-- 背景 (Issue #119):
--   altpocket は read-later サービスでありながら、現状はアイテムが「未読 / 既読 /
--   アーカイブ済み」のユーザー可視な状態を持たない。items.fetch_status (pending /
--   fetching / success / failed) は本文取得の進捗を示すのみで、読了消化や整理の
--   指標として機能しない。本マイグレーションでは items に 3 値 enum status
--   (unread / read / archived) を追加し、Web UI / MCP から状態管理できる土台を提供する。
--   状態軸 (status) と既存 fetch 軸 (fetch_status) は独立した 2 軸として扱う
--   (互いに上書きしない / 設計 design.md "Architecture Integration" 参照)。
--
-- 適用順:
--   migrations/001_init.sql .. 006_item_tag_display_name.sql の後に番号順で psql -f
--   により適用する (forward-only。本プロジェクトは down マイグレーションを必須としない)。
--   既存マイグレーション 001..006 の中身は本マイグレーションでは変更しない。
--
-- 冪等性パターン:
--   1. カラム追加は ADD COLUMN IF NOT EXISTS で再適用しても安全。
--   2. CHECK 制約は PostgreSQL 16 に ADD CONSTRAINT IF NOT EXISTS が存在しないため、
--      DO $$ BEGIN ... EXCEPTION WHEN duplicate_object THEN NULL; END $$; ブロックで
--      ALTER TABLE ... ADD CONSTRAINT を包み、再適用時の duplicate_object を吸収する。
--   3. index 作成は CREATE INDEX IF NOT EXISTS で同様に冪等。
--
-- backfill 戦略:
--   ADD COLUMN ... NOT NULL DEFAULT 'unread' により既存全行が 'unread' で
--   自動的に backfill される (PostgreSQL 11+ の高速 default 最適化により、
--   既存行を書き換えずに NOT NULL を成立させる)。追加の UPDATE 文は不要。
--   これにより Req 1.3 (既存アイテムを unread として返す) / Req 6.1
--   (データ消失や状態未設定アイテムを生まない) を満たす。

-- 1) status カラム追加。既存行は DEFAULT 'unread' で自動 backfill される。
ALTER TABLE items
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unread';

-- 2) CHECK 制約を冪等に追加。PostgreSQL 16 には ADD CONSTRAINT IF NOT EXISTS が
--    存在しないため、duplicate_object 例外を吸収する DO ブロックで包む。
DO $$
BEGIN
  ALTER TABLE items
    ADD CONSTRAINT items_status_check
    CHECK (status IN ('unread', 'read', 'archived'));
EXCEPTION
  WHEN duplicate_object THEN NULL;
END
$$;

-- 3) status フィルタ付き一覧クエリ (?status=unread 既定) の seq scan を回避するための
--    複合 index。既存 items_user_created_idx (user_id, created_at DESC) は status を
--    WHERE しないクエリ / worker クエリ向けに維持する (Performance & Scalability 節)。
CREATE INDEX IF NOT EXISTS items_user_status_idx
  ON items (user_id, status, created_at DESC);
