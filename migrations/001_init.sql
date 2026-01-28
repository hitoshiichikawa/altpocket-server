CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  google_sub TEXT NOT NULL UNIQUE,
  email TEXT,
  name TEXT,
  avatar_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  canonical_url TEXT NOT NULL,
  canonical_hash TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  excerpt TEXT NOT NULL DEFAULT '',
  fetch_status TEXT NOT NULL DEFAULT 'pending',
  fetch_error TEXT NOT NULL DEFAULT '',
  fetched_at TIMESTAMPTZ,
  refetch_requested BOOLEAN NOT NULL DEFAULT FALSE,
  fetch_attempts INT NOT NULL DEFAULT 0,
  last_fetch_attempt_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT items_fetch_status_check CHECK (fetch_status IN ('pending', 'fetching', 'success', 'failed')),
  UNIQUE (user_id, canonical_hash)
);

CREATE TABLE item_contents (
  item_id UUID PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  content_full TEXT NOT NULL DEFAULT '',
  content_search TEXT NOT NULL DEFAULT '',
  content_bytes INT NOT NULL DEFAULT 0
);

CREATE TABLE tags (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  normalized_name TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE item_tags (
  item_id UUID NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (item_id, tag_id)
);

CREATE INDEX items_user_created_idx ON items (user_id, created_at DESC);
CREATE INDEX item_tags_tag_idx ON item_tags (tag_id);
CREATE INDEX item_tags_item_idx ON item_tags (item_id);

CREATE INDEX items_title_trgm_idx ON items USING gin (title gin_trgm_ops);
CREATE INDEX items_excerpt_trgm_idx ON items USING gin (excerpt gin_trgm_ops);
CREATE INDEX items_canonical_url_trgm_idx ON items USING gin (canonical_url gin_trgm_ops);
CREATE INDEX item_contents_search_trgm_idx ON item_contents USING gin (content_search gin_trgm_ops);
CREATE INDEX tags_normalized_trgm_idx ON tags USING gin (normalized_name gin_trgm_ops);
