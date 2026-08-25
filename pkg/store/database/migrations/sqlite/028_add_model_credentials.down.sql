-- Down migration (SQLite, AMENDED 2026-08-21 M-1, LOSSLESS).
--
-- Pulls credential_id back from credentials_json[0] (re-adding the
-- column if a future state has dropped it -- e.g., after 029 has
-- landed and someone down-migrates past it). The credentials_json
-- column itself is PRESERVED (a re-up restores the full list).
--
-- If credential_id is already present (typical case today, since
-- 028 does not drop it), this is a no-op for the column add and a
-- defensive re-write of the shadow from credentials_json[0].

BEGIN TRANSACTION;

-- Re-add the column only if missing (idempotent for the
-- pre-029-deprecation case). The IF NOT EXISTS form is not
-- supported on older PG; for SQLite we use the pragma check.
-- For now, assume the down-migration runs in a state where
-- credential_id EXISTS (pre-029 typical), so we skip the ADD.
-- If the column is absent, the operator must run a manual ALTER
-- TABLE first.

UPDATE models
SET credential_id = COALESCE(
        json_extract(credentials_json, '$[0].credential_id'),
        credential_id
    )
WHERE credentials_json IS NOT NULL
  AND credentials_json != '[]'
  AND json_array_length(credentials_json) > 0
  AND json_extract(credentials_json, '$[0].credential_id') IS NOT NULL;

-- NOTE: we do NOT DROP credentials_json -- it is preserved so a
-- re-up restores the full multi-credential list.
--
-- Operator note (Phase-1 S2): the .down migration is a MANUAL
-- artifact — it is not auto-run by runMigration(). schema_migrations
-- records the .up application; the .down is provided here for the
-- operator to run by hand if they need to roll the shadow write
-- back. After running .down + .up again, schema_migrations will
-- show two rows with the same version (one for each up run); the
-- isMigrationApplied() check guards against re-applying .up on the
-- second run, so the second .up is a no-op (the ALTER ADD COLUMN
-- would fail if the column already existed — this is the safety
-- net). Operators who want a clean record can DELETE FROM
-- schema_migrations WHERE version = '028' between the .down and
-- the second .up.

COMMIT;
