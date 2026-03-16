-- Migration 009: Add ultimate model configuration storage

ALTER TABLE configs ADD COLUMN ultimate_model_json TEXT NOT NULL DEFAULT '{"model_id":"","max_hash":100}';
