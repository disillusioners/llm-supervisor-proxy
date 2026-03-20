-- Migration 014: Add log_raw_upstream_response column to configs table
ALTER TABLE configs ADD COLUMN log_raw_upstream_response BOOLEAN NOT NULL DEFAULT FALSE;
