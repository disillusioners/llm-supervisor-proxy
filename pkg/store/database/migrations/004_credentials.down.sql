-- 004_credentials.down.sql
-- Rollback credentials table

DROP INDEX IF EXISTS idx_credentials_provider;
DROP INDEX IF EXISTS idx_models_credential_id;
ALTER TABLE models DROP COLUMN credential_id;
DROP TABLE IF EXISTS credentials;
