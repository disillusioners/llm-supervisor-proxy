-- Migration 031 (down, Postgres): DROP the models group columns
-- (load-balance-groups commission / §7.1). Plain DROP COLUMN
-- (mirrors the SQLite twin / 023/030 form).
--
-- Operator note: this down is a MANUAL artifact. It is not
-- auto-run by runMigration(). See the sqlite 031 down file for
-- the full operator note (schema_migrations hygiene step).

ALTER TABLE models DROP COLUMN members_json;
ALTER TABLE models DROP COLUMN shadow_model_id;
