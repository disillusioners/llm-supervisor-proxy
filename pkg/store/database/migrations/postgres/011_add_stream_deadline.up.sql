-- Migration 011: Add stream_deadline field to configs table
ALTER TABLE configs ADD COLUMN stream_deadline_ms INTEGER NOT NULL DEFAULT 110000;
