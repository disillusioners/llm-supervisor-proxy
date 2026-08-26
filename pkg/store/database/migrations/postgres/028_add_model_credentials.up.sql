-- Migration 028 (Postgres, AMENDED 2026-08-21 M-1): ADD + backfill +
-- same-statement shadow write to credential_id. NO DROP COLUMN
-- (deferred to 029+).
--
-- The transaction is the repo's FIRST file-level BEGIN...COMMIT
-- through pgx/v5 ExecContext; MANDATORY PG-gated test required.

BEGIN;

-- 1. Add the new column.
ALTER TABLE models
    ADD COLUMN credentials_json TEXT NOT NULL DEFAULT '[]';

-- 2. Backfill + shadow write (same statement, JSONB-array form).
UPDATE models
SET credentials_json = (
        jsonb_build_array(
            jsonb_build_object(
                'credential_id', credential_id,
                'weight',        1,
                'position',      0
            )
        )::text
    ),
    credential_id = credential_id  -- shadow (no-op today; load-bearing for 029)
WHERE credential_id IS NOT NULL
  AND credential_id != '';

-- 3. NO DROP COLUMN. credential_id stays; DROP moves to 029+.

COMMIT;
