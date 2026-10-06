-- Migration 029 (down, Postgres): DROP image_count from the two
-- hourly-usage tables (ImgGen Models commission / BE-M1, Architect
-- Amendment 13). Plain DROP COLUMN (PG supports IF NOT EXISTS but
-- we mirror the 023 form for consistency with the SQLite twin).
--
-- Operator note: this down is a MANUAL artifact. It is not
-- auto-run by runMigration(). See the sqlite 029 down file for
-- the full operator note.

ALTER TABLE token_hourly_usage DROP COLUMN image_count;
ALTER TABLE model_hourly_usage DROP COLUMN image_count;
