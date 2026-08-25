-- Migration 028: Add models.credentials_json (ordered, weighted list)
-- and backfill from existing models.credential_id rows.
--
-- **AMENDED 2026-08-21 (M-1)**: this migration does NOT drop
-- models.credential_id. The column is kept as a DERIVED SHADOW --
-- the same UPDATE statement that writes credentials_json also
-- writes credential_id (= credentials_json[0].credential_id) so
-- that legacy binaries and external tooling keep reading a
-- consistent value. DROP INDEX + DROP COLUMN are deferred to
-- migration 029+ behind a release-note deprecation window.
--
-- The transaction is the repo's FIRST file-level BEGIN...COMMIT
-- pair through the SQLite/modernc.org/sqlite v1.46.1 driver +
-- pgx/v5 ExecContext pairing. A MANDATORY PG-gated test must
-- PROVE the transaction commits on both dialects (see testing
-- section); do not ship without it.

BEGIN TRANSACTION;

-- 1. Add the new column.
ALTER TABLE models ADD COLUMN credentials_json TEXT NOT NULL DEFAULT '[]';

-- 2. Backfill + shadow write (same statement).
--    For every row with a non-empty credential_id:
--      credentials_json = [{"credential_id", "weight":1, "position":0}]
--      credential_id   = credential_id   (shadow, no-op)
--    The shadow write is a tautology; it lives in the same
--    UPDATE so that any future writer that touches only
--    credentials_json is forced (by code review) to also touch
--    credential_id. Rows with credential_id = '' or NULL stay
--    with credentials_json = '[]' and shadow = ''.
UPDATE models
SET credentials_json = '[{"credential_id":"' || credential_id ||
                       '","weight":1,"position":0}]',
    credential_id   = credential_id  -- shadow (no-op today; load-bearing for 029)
WHERE credential_id IS NOT NULL
  AND credential_id != '';

-- 3. NO DROP COLUMN. credential_id stays; DROP moves to 029+.

COMMIT;
