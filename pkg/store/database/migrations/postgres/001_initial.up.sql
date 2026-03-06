-- Migration 001: Create configs and models tables

-- Create configs table with PostgreSQL syntax
CREATE TABLE IF NOT EXISTS configs (
    id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version TEXT NOT NULL DEFAULT '1.0',
    upstream_url TEXT NOT NULL DEFAULT 'http://localhost:4001',
    port INTEGER NOT NULL DEFAULT 4321,
    idle_timeout_ms BIGINT NOT NULL DEFAULT 60000,
    max_generation_time_ms BIGINT NOT NULL DEFAULT 300000,
    max_upstream_error_retries INTEGER NOT NULL DEFAULT 1,
    max_idle_retries INTEGER NOT NULL DEFAULT 2,
    max_generation_retries INTEGER NOT NULL DEFAULT 1,
    loop_detection_json JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create models table
CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    fallback_chain_json JSONB NOT NULL DEFAULT '[]',
    truncate_params_json JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create index
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled) WHERE enabled = true;

-- Ensure default config row exists
INSERT INTO configs (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;
