CREATE TABLE IF NOT EXISTS credential_groups (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    provider     TEXT NOT NULL,
    members_json TEXT NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_credential_groups_provider ON credential_groups(provider);
