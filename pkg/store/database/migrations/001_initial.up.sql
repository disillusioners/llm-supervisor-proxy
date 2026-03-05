-- 001_initial.up.sql
-- Initial schema for config and models storage (SQLite)

-- App configuration (single row table)
CREATE TABLE IF NOT EXISTS configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),  -- Enforces single row
    
    -- Core settings
    version TEXT NOT NULL DEFAULT '1.0',
    upstream_url TEXT NOT NULL DEFAULT 'http://localhost:4001',
    upstream_credential_id TEXT,
    port INTEGER NOT NULL DEFAULT 4321,
    
    -- Timeouts (stored as milliseconds)
    idle_timeout_ms INTEGER NOT NULL DEFAULT 60000,
    max_generation_time_ms INTEGER NOT NULL DEFAULT 300000,
    
    -- Retries
    max_upstream_error_retries INTEGER NOT NULL DEFAULT 1,
    max_idle_retries INTEGER NOT NULL DEFAULT 2,
    max_generation_retries INTEGER NOT NULL DEFAULT 1,
    
    -- Loop detection config stored as JSON
    loop_detection_json TEXT NOT NULL DEFAULT '{}',
    
    -- Metadata
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Model configurations
CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,  -- SQLite uses INTEGER for boolean
    fallback_chain_json TEXT NOT NULL DEFAULT '[]',
    truncate_params_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Index for enabled models lookup
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled);
