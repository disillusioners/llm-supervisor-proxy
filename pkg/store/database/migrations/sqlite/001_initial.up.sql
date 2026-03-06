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
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Create models table
CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    fallback_chain_json TEXT NOT NULL DEFAULT '[]',
    truncate_params_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Create index
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled);

-- Ensure default config row exists
INSERT OR IGNORE INTO configs (id) VALUES (1);
