-- MCP API Keys for Bearer Token authentication
CREATE TABLE mcp_api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_mcp_api_keys_user_id ON mcp_api_keys(user_id);
CREATE INDEX idx_mcp_api_keys_key_hash ON mcp_api_keys(key_hash);
