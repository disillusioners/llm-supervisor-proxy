-- Migration 029 (Postgres): Add image_count column to
-- token_hourly_usage + model_hourly_usage (ImgGen Models
-- commission / BE-M1).
--
-- See the sqlite 029 file for the full rationale. The Postgres
-- form is identical aside from the dialect; both NOT NULL DEFAULT
-- 0 so existing rows are backfilled to zero.
--
-- Migration registry note (Architect Amendment 13): the
-- models.credential_id DROP COLUMN promised in 028's comments
-- now lands at 030+ to prevent numbering conflation.

ALTER TABLE token_hourly_usage ADD COLUMN image_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_hourly_usage ADD COLUMN image_count INTEGER NOT NULL DEFAULT 0;
