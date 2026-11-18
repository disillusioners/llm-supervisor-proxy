-- Migration 034 (PostgreSQL): see the sqlite twin for the full rationale.
BEGIN;

ALTER TABLE auth_tokens ADD COLUMN IF NOT EXISTS allowed_groups TEXT DEFAULT NULL;

-- Data repartition (idempotent). Row-by-row guard (PD-2): a DO $$
-- block catches invalid_text_representation per row, so a malformed
-- TEXT row beginning with '[' (e.g. '[broken') is skipped, never
-- aborting the in-process boot migration. Version-agnostic: no
-- dependency on PG 16+ pg_input_is_valid. json_agg over zero rows
-- returns NULL -> ABSENT (dialect parity via SQLite NULLIF, PD-4).
DO $$
DECLARE
  r RECORD;
BEGIN
  FOR r IN
    SELECT id FROM auth_tokens
     WHERE allowed_models IS NOT NULL
       AND btrim(allowed_models) <> ''
       AND allowed_models ~ '^\s*\['
  LOOP
    BEGIN
      UPDATE auth_tokens t
      SET allowed_groups = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                            WHERE v IN (SELECT id FROM models WHERE kind = 'group')),
          allowed_models = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                            WHERE v NOT IN (SELECT id FROM models WHERE kind = 'group'))
      WHERE t.id = r.id
        AND t.allowed_models::jsonb <> '[]'::jsonb
        AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                    WHERE v IN (SELECT id FROM models WHERE kind = 'group'));
    EXCEPTION WHEN invalid_text_representation THEN
      -- malformed row: SKIP IT, never abort. Migrations run in-process at
      -- boot (pkg/store/database/init.go:19), so an unhandled error here
      -- CrashLoops the pod and re-runs the same failing statement forever.
      -- RAISE WARNING (not NOTICE) so the skip is visible in pod stdout at
      -- the default log level. The row is left byte-identical; the new
      -- scanner classifies it as INVALID => DENY-ALL (fail-closed).
      RAISE WARNING '034: skipping malformed allowed_models on token % (left untouched; the scanner denies it)', r.id;
    END;
  END LOOP;
END $$;

COMMIT;
