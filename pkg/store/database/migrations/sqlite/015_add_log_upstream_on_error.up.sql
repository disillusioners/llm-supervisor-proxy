-- Migration 015: Add log_raw_upstream_on_error column to configs table
ALTER TABLE configs ADD COLUMN log_raw_upstream_on_error INTEGER NOT NULL DEFAULT 0;
