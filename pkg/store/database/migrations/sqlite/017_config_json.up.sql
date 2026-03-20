-- WARNING: This migration destroys all existing configuration data!
-- This is acceptable for pre-production environments.
-- The config will be repopulated from environment variables on next startup.

DROP TABLE IF EXISTS configs;

CREATE TABLE configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Ensure only one row can exist
INSERT INTO configs (id, config_json) VALUES (1, '{}');
