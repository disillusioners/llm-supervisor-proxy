-- 005_upstream_token.down.sql
-- Remove upstream_token column from configs table

-- SQLite doesn't support DROP COLUMN, so we need to recreate the table
-- For PostgreSQL:
-- ALTER TABLE configs DROP COLUMN upstream_token;

-- For SQLite, we'll just leave the column (no-op for rollback)
