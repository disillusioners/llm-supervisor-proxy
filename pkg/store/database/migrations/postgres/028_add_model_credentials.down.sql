-- Down migration (Postgres, AMENDED 2026-08-21 M-1, LOSSLESS).
-- Preserves credentials_json so a re-up restores the full list.

BEGIN;

UPDATE models
SET credential_id = COALESCE(
        (credentials_json::JSONB -> 0 ->> 'credential_id'),
        credential_id
    )
WHERE credentials_json IS NOT NULL
  AND credentials_json != '[]'
  AND jsonb_array_length(credentials_json::JSONB) > 0;

-- NOTE: we do NOT DROP credentials_json.

COMMIT;
