-- Migration 034 (down, PostgreSQL): DOCUMENTATION-ONLY artifact. No
-- down-runner exists in this repo; rollback = restore backup.
--
-- OPERATOR NOTE: running this by hand REQUIRES deleting the
-- schema_migrations row for version '034' (migrate.go:103), or the runner
-- treats 034 as applied and never re-runs it.
--
-- ROLLBACK ORDERING INVARIANT (ADR §7.1 — blocking runbook rule): reverse
-- the data repartition BEFORE the image/helm rollback. A pre-034 binary
-- treats an empty allowed_models as a wildcard, so image-first rollback
-- would promote every migrated narrow token to full access.

BEGIN;

-- 1) Reverse the data repartition: group ids back into allowed_models.
-- A row whose allowed_groups is malformed is skipped rather than aborting
-- the whole reversal (same version-agnostic guard as the up migration).
DO $$
DECLARE
  r RECORD;
BEGIN
  FOR r IN
    SELECT id FROM auth_tokens
     WHERE allowed_groups IS NOT NULL
       AND btrim(allowed_groups) <> ''
       AND allowed_groups ~ '^\s*\['
  LOOP
    BEGIN
      UPDATE auth_tokens t
      SET allowed_models = (SELECT json_agg(v) FROM (
              SELECT value AS v FROM jsonb_array_elements_text(t.allowed_models::jsonb)
              UNION ALL
              SELECT value AS v FROM jsonb_array_elements_text(t.allowed_groups::jsonb)) u),
          allowed_groups = NULL
      WHERE t.id = r.id;
    EXCEPTION WHEN invalid_text_representation THEN
      -- malformed grant row: leave untouched (do not guess a grant list).
      NULL;
    END;
  END LOOP;
END $$;

-- 2) Drop the column.
ALTER TABLE auth_tokens DROP COLUMN IF EXISTS allowed_groups;

COMMIT;
