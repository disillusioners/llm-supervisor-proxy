-- Migration 030 (down, Postgres): DROP the models.kind discriminator
-- column (ImgGen Models commission ship-blocker fix, 2026-10-06).
-- Plain DROP COLUMN (mirrors the SQLite twin / 023 form; PG supports
-- IF NOT EXISTS but we keep the shape consistent).
--
-- Operator note: this down is a MANUAL artifact. It is not
-- auto-run by runMigration(). See the sqlite 030 down file for
-- the full operator note.

ALTER TABLE models DROP COLUMN kind;
