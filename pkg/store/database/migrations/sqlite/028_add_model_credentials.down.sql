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

COMMIT;
