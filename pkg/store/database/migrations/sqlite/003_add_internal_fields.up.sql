-- Migration 003: Add internal upstream fields to models
ALTER TABLE models ADD COLUMN internal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE models ADD COLUMN internal_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_api_key TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_model TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_key_version INTEGER NOT NULL DEFAULT 1;
CREATE INDEX IF NOT EXISTS idx_models_internal ON models(internal);
