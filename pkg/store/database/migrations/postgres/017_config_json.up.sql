-- WARNING: This migration destroys all existing configuration data!
-- This is acceptable for pre-production environments.
-- The config will be repopulated from environment variables on next startup.

DROP TABLE IF EXISTS configs;

CREATE TABLE configs (
    id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    config_json JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Ensure only one row can exist
INSERT INTO configs (id, config_json) VALUES (1, '{}');
