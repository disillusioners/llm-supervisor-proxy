-- Migration 005: Add credentials table and credential_id to models
CREATE TABLE IF NOT EXISTS credentials (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    api_key TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
ALTER TABLE models ADD COLUMN credential_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_models_credential_id ON models(credential_id);
CREATE INDEX IF NOT EXISTS idx_credentials_provider ON credentials(provider);
