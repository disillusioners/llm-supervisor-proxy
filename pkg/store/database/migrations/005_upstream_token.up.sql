-- 005_upstream_token.up.sql
-- Add upstream_token column to configs table

ALTER TABLE configs ADD COLUMN upstream_token TEXT NOT NULL DEFAULT '';
