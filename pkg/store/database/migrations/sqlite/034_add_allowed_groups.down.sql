-- Migration 034 (down): DOCUMENTATION-ONLY artifact. No down-runner
-- exists in this repo (precedent 033_add_credential_groups.down.sql:1-3:
-- "No down-runner exists in this repo (025/026/027 have NO down file);
-- rollback = restore backup.").
--
-- OPERATOR NOTE: if you run this by hand you MUST also delete the
-- schema_migrations row for version '034' (migrate.go:103), or the
-- runner treats 034 as applied and never re-runs it.
--
-- ROLLBACK ORDERING INVARIANT (ADR §7.1 — blocking runbook rule, never
-- optional): the REVERSE REPARTITION below MUST run BEFORE any image /
-- helm rollback to a pre-034 binary. The pre-034 binary reads only
-- allowed_models and treats len==0 (NULL or '[]') as a WILDCARD, so
-- rolling the image back first would silently promote every migrated
-- narrow token to full access on the public /v1/* endpoints. Running the
-- reverse first, while the NEW binary is still live, is safe: a reversed
-- row has allowed_models PRESENT, so it stays RESTRICTED (target-only).

BEGIN;

-- 1) REVERSE the data repartition (deterministic: group ids go back into
--    allowed_models; the union is order-stable enough for a permission
--    list because matching is set membership, order-insensitive).
UPDATE auth_tokens
SET allowed_models = (SELECT json_group_array(value) FROM (
        SELECT j.value FROM json_each(allowed_models) j
        UNION ALL
        SELECT j.value FROM json_each(allowed_groups) j)),
    allowed_groups = NULL
WHERE allowed_groups IS NOT NULL AND json_valid(allowed_groups);

-- 2) Drop the column (SQLite has no IF EXISTS for DROP COLUMN).
ALTER TABLE auth_tokens DROP COLUMN allowed_groups;

COMMIT;
