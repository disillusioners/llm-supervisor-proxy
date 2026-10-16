-- Migration 031 (Postgres): Add group columns to `models`
-- (load-balance-groups commission / §7.1).
--
-- See the sqlite 031 file for the full rationale. The Postgres
-- form is identical aside from the dialect: two nullable TEXT
-- columns, no DEFAULT, no backfill — NULL members_json normalizes
-- to an empty member list on load via coalesce(members_json, '[]'),
-- NULL shadow_model_id to '' via coalesce(shadow_model_id, '').

ALTER TABLE models ADD COLUMN members_json TEXT;
ALTER TABLE models ADD COLUMN shadow_model_id TEXT;
