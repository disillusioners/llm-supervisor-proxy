-- Migration 006: Add upstream_credential_id to configs
ALTER TABLE configs ADD COLUMN upstream_credential_id TEXT NOT NULL DEFAULT '';
