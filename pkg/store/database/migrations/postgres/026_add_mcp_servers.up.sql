CREATE TABLE IF NOT EXISTS mcp_servers (
    id              TEXT PRIMARY KEY,
    name            TEXT    NOT NULL UNIQUE,
    description     TEXT    DEFAULT '',
    upstream_url    TEXT    NOT NULL,
    transport_type  TEXT    NOT NULL DEFAULT 'streamable_http',
    auth_type       TEXT    DEFAULT 'none',
    auth_token      TEXT    DEFAULT '',
    headers         TEXT    DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled);
