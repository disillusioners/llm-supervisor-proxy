-- Migration 030 (down, SQLite): DROP the models.kind discriminator
-- column (ImgGen Models commission ship-blocker fix, 2026-10-06).
-- Plain DROP COLUMN (the 023 form) — NOT 022's
-- "DROP COLUMN IF EXISTS"; SQLite's grammar has no IF EXISTS.
--
-- Operator note: this down is a MANUAL artifact. It is not
-- auto-run by runMigration(). schema_migrations records the
-- .up application. Operators who run the .down + re-apply
-- .up should DELETE FROM schema_migrations WHERE version =
-- '030' between the two steps to keep the record clean.
--
-- Rolling back reverts to the ship-blocker behavior (image-gen
-- kind is memory-only again); ModelConfig.Kind in Go is untouched.

ALTER TABLE models DROP COLUMN kind;
