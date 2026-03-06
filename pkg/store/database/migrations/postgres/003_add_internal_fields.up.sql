-- Migration 003: Add internal upstream fields to models
ALTER TABLE models ADD COLUMN IF NOT EXISTS internal BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE models ADD COLUMN IF NOT EXISTS internal_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN IF NOT EXISTS internal_api_key TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN IF NOT EXISTS internal_base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN IF NOT EXISTS internal_model TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN IF NOT EXISTS internal_key_version INTEGER NOT NULL DEFAULT 1;
CREATE INDEX IF NOT EXISTS idx_models_internal ON models(internal) WHERE internal = true;
