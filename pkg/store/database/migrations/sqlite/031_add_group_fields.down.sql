-- Migration 031 (down, SQLite): DROP the models group columns
-- (load-balance-groups commission / §7.1). Plain DROP COLUMN (the
-- 023/030 form) — NOT "DROP COLUMN IF EXISTS"; SQLite's grammar
-- has no IF EXISTS.
--
-- Operator note (same as 030's): this down is a MANUAL artifact.
-- It is not auto-run by runMigration(). schema_migrations records
-- the .up application. Operators who run the .down + re-apply
-- .up should DELETE FROM schema_migrations WHERE version = '031'
-- between the two steps to keep the record clean.
--
-- Rolling back drops group membership + claims (groups become
-- unsavable — every write path requires the columns); the
-- ModelConfig fields in Go are untouched.

ALTER TABLE models DROP COLUMN members_json;
ALTER TABLE models DROP COLUMN shadow_model_id;
