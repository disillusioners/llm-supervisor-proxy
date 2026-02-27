-- 002_internal_upstream.down.sql
-- Rollback internal upstream fields

DROP INDEX IF EXISTS idx_models_internal;
ALTER TABLE models DROP COLUMN internal_key_version;
ALTER TABLE models DROP COLUMN internal_model;
ALTER TABLE models DROP COLUMN internal_base_url;
ALTER TABLE models DROP COLUMN internal_api_key;
ALTER TABLE models DROP COLUMN internal_provider;
ALTER TABLE models DROP COLUMN internal;
