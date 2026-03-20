-- Migration 016: Add log_raw_upstream_max_kb column to configs table
ALTER TABLE configs ADD COLUMN log_raw_upstream_max_kb INTEGER NOT NULL DEFAULT 1024;
