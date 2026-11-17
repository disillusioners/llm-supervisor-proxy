-- Migration 033: credential groups (management-only bundling of same-provider credentials)
CREATE TABLE IF NOT EXISTS credential_groups (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    provider     TEXT NOT NULL,
    members_json TEXT NOT NULL DEFAULT '[]',
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_credential_groups_provider ON credential_groups(provider);
