-- 003_auth_tokens.down.sql
-- Rollback auth tokens table

DROP INDEX IF EXISTS idx_auth_tokens_hash;
DROP TABLE IF EXISTS auth_tokens;
