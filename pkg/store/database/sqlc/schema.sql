-- Combined schema for sqlc code generation
-- This file represents the current state after all migrations

-- Migration 001: Create configs and models tables

-- Create configs table
CREATE TABLE IF NOT EXISTS configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version TEXT NOT NULL DEFAULT '1.0',
    upstream_url TEXT NOT NULL DEFAULT 'http://localhost:4001',
    port INTEGER NOT NULL DEFAULT 4321,
    idle_timeout_ms INTEGER NOT NULL DEFAULT 60000,
    max_generation_time_ms INTEGER NOT NULL DEFAULT 300000,
    max_upstream_error_retries INTEGER NOT NULL DEFAULT 1,
    max_idle_retries INTEGER NOT NULL DEFAULT 2,
    max_generation_retries INTEGER NOT NULL DEFAULT 1,
    loop_detection_json TEXT NOT NULL DEFAULT '{}',
    tool_repair_json TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    upstream_credential_id TEXT
);

-- Create models table
CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    fallback_chain_json TEXT NOT NULL DEFAULT '[]',
    truncate_params_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    internal INTEGER NOT NULL DEFAULT 0,
    internal_provider TEXT,
    internal_api_key TEXT,
    internal_base_url TEXT,
    internal_model TEXT,
    internal_key_version INTEGER NOT NULL DEFAULT 0,
    credential_id TEXT,
    release_stream_chunk_deadline INTEGER NOT NULL DEFAULT 0
);

-- Migration 002: Add buffer field
ALTER TABLE configs ADD COLUMN buffer_size INTEGER NOT NULL DEFAULT 8192;

-- Migration 003: Add internal fields (already in schema above)

-- Migration 004: Add auth tokens
CREATE TABLE IF NOT EXISTS auth_tokens (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    expires_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by TEXT NOT NULL
);

-- Migration 005: Add credentials
CREATE TABLE IF NOT EXISTS credentials (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    api_key TEXT NOT NULL,
    base_url TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Migration 006: Add upstream credential (already in schema above)

-- Migration 007: Add tool repair (already in configs above via tool_repair_json)

-- Migration 008: Add release stream chunk deadline (already in models above)
