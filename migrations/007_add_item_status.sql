-- items.status: ユーザー可視な 3 値状態（unread / read / archived）を追加する。
--
-- 背景 (Issue #119):
--   altpocket は read-later サービスでありながら、現状 items に「未読 / 既読 /
--   アーカイブ」を表すユーザー可視な状態を持たない。items.fetch_status
--   （pending / fetching / success / failed）は本文取得の進捗を示すだけで、
--   読了消化や整理の指標として機能していない。本マイグレーションでは items に
--   3 値 enum `status`（unread / read / archived）カラムを追加し、forward-only な
--   形で既存データを `unread` に backfill する。fetch_status とは独立した
--   2 軸モデルとして運用し、worker 側経路は本変更の影響を受けない（Req 1.6 /
--   Req 6.2 の独立性保証）。
--
-- 適用順:
--   migrations/001_init.sql .. 006_item_tag_display_name.sql の後に番号順で
--   psql -f により適用する（forward-only。本プロジェクトは down マイグレーションを
--   必須としない）。既存マイグレーション（001..006）の中身は本マイグレーションで
--   書き換えない。
--
-- 冪等性パターン:
--   1) ADD COLUMN IF NOT EXISTS / CREATE INDEX IF NOT EXISTS により列・インデックスは
--      再適用しても安全。既存行は DEFAULT 'unread' で backfill される（Req 1.3 /
--      Req 6.1: データ消失なし）。
--   2) CHECK 制約は PostgreSQL 16 に `ADD CONSTRAINT IF NOT EXISTS` 構文が存在しない
--      ため、DO $$ ... EXCEPTION WHEN duplicate_object ... $$ ブロックで包んで
--      duplicate_object を吸収する形で冪等化する。再適用時は同名 CHECK が既に
--      存在するため duplicate_object が raise され、ハンドラで握り潰す。

-- 1) status カラム追加。既存全行は DEFAULT 'unread' により backfill される
--    （Req 1.1 / Req 1.2 / Req 1.3 / Req 6.1）。
ALTER TABLE items
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unread';

-- 2) CHECK 制約を冪等に追加。PostgreSQL 16 には ADD CONSTRAINT IF NOT EXISTS が
--    存在しないため、duplicate_object 例外を握り潰すことで再適用安全にする
--    （Req 1.5: enum 範囲外の値を DB 層で拒否 / 二重防御）。
DO $$
BEGIN
  ALTER TABLE items
    ADD CONSTRAINT items_status_check
    CHECK (status IN ('unread', 'read', 'archived'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END
$$;

-- 3) `?status=unread` 既定で頻発する一覧クエリ向けの compound index
--    （NFR 1.1 / NFR 1.2: 一覧表示・タブ切替のパフォーマンス維持）。
--    既存の items_user_created_idx (user_id, created_at DESC) は維持し、
--    status を WHERE しないクエリ（worker / 全件取得経路）でも引き続き使われる。
CREATE INDEX IF NOT EXISTS items_user_status_idx
  ON items (user_id, status, created_at DESC);
