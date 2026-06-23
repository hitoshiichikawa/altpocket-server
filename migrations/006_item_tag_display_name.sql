-- Per-user タグ表示名を item_tags へ移設し、マルチテナント表示名漏洩を修正する。
--
-- 背景 (Issue #115 / PR #137 codex [high] レビュー):
--   タグの表示名は従来グローバル共有の tags.name に保存されていた。tags は
--   normalized_name に UNIQUE 制約を持つ「全ユーザー共有」テーブルのため、
--   同じ normalized_name を複数ユーザーが異なる casing / 表示名で持つ場合に、
--   先に行を作ったユーザーの表示名が後続ユーザーのチップ・カード・サイドバーへ
--   漏洩する（multi-tenant isolation 崩壊 / AC 1.3 破綻）。また CreateItem /
--   PatchItem の ON CONFLICT (normalized_name) DO NOTHING が no-op になるため、
--   既存行があると入力された表示名・casing 変更が保存されなかった。
--
-- 本マイグレーション:
--   item_tags（user-scoped な items への join 行 = 実質 per-user）へ display_name
--   列を追加する。表示名は以後ここに保存し、tags.name は legacy（参照しない正準外
--   フィールド）に縮退する。tags は normalized_name によるグローバル同一性
--   （フィルタ用キー）に専念する。
--
-- 適用順:
--   migrations/001_init.sql .. 005 の後に番号順で psql -f により適用する
--   （forward-only。本プロジェクトは down マイグレーションを必須としない）。
--
-- 冪等性:
--   ADD COLUMN IF NOT EXISTS により再適用しても安全。backfill UPDATE は
--   display_name が未設定（空文字列 = 列追加直後の DEFAULT）の行のみ対象とするため、
--   既に per-user 表示名が保存された行を上書きしない。

-- 1) per-item（= per-user）表示名カラムを追加。既存行は DEFAULT '' で埋まる。
ALTER TABLE item_tags
  ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';

-- 2) 既存データの backfill: 列追加直後で display_name がまだ未設定（空文字列）の
--    行に対してのみ、当時の共有 tags.name を初期表示名として転記する。
--    これにより本機能導入前の見た目（共有表示名）が維持され、以後ユーザーが
--    再保存・編集すると per-user 表示名へ更新される（差分等価な移行）。
UPDATE item_tags it
SET display_name = t.name
FROM tags t
WHERE it.tag_id = t.id
  AND it.display_name = ''
  AND t.name <> '';
