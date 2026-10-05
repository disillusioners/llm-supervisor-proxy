-- Migration 029 (down, SQLite): DROP image_count from the two
-- hourly-usage tables (ImgGen Models commission / BE-M1,
-- Architect Amendment 13). Use plain DROP COLUMN (the 023 form)
-- — NOT 022's "DROP COLUMN IF EXISTS" — because SQLite's grammar
-- has no IF EXISTS. The latent bug in 022 never fired because
-- downs are manual-run artifacts.
--
-- Operator note: this down is a MANUAL artifact. It is not
-- auto-run by runMigration(). schema_migrations records the
-- .up application. Operators who run the .down + re-apply
-- .up should DELETE FROM schema_migrations WHERE version =
-- '029' between the two steps to keep the record clean.

ALTER TABLE token_hourly_usage DROP COLUMN image_count;
ALTER TABLE model_hourly_usage DROP COLUMN image_count;
