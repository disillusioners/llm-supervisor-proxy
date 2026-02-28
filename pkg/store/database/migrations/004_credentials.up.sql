-- 004_credentials.up.sql
-- Add credentials table and migrate from inline API keys

-- Create credentials table
CREATE TABLE IF NOT EXISTS credentials (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    api_key TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Add credential_id column to models table
ALTER TABLE models ADD COLUMN credential_id TEXT NOT NULL DEFAULT '';

-- Create index for credential lookup
CREATE INDEX IF NOT EXISTS idx_models_credential_id ON models(credential_id);
CREATE INDEX IF NOT EXISTS idx_credentials_provider ON credentials(provider);
