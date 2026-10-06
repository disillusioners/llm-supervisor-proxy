-- Migration 030 (Postgres): Add `kind` discriminator column to
-- `models` (ImgGen Models commission — ship-blocker fix,
-- 2026-10-06).
--
-- See the sqlite 030 file for the full rationale. The Postgres
-- form is identical aside from the dialect: nullable TEXT, no
-- DEFAULT, no backfill — NULL/'' normalize to chat on load via
-- coalesce(kind, '').
--
-- Registry note: the models.credential_id shadow-column drop
-- reserved for "030+" in 029's header moves to a later number;
-- 030 is the kind column.

ALTER TABLE models ADD COLUMN kind TEXT;
