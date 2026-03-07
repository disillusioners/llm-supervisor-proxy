-- Migration 007: Add tool repair configuration storage

ALTER TABLE configs ADD COLUMN tool_repair_json TEXT NOT NULL DEFAULT '{}';
