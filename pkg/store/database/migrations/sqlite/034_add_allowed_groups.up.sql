-- Migration 034: token permission model — allowed_groups (group reach)
-- + pre-ship data repartition (plan §5.4; decisions.md Q2 (b)).
--
-- SQLite has NO `ADD COLUMN IF NOT EXISTS`; idempotency comes from
-- schema_migrations (migrate.go:103 isMigrationApplied). Precedent:
-- 022_add_allowed_models.up.sql:4-5, 030_add_model_kind.up.sql:24-27.
-- The PRAGMA in 022 is a NO-OP READ, not a guard — do not copy it as one.
--
-- TRANSACTION WRAPPER (028/032 precedent): modernc.org/sqlite does not
-- auto-rollback a partial multi-statement Exec — wrap explicitly.
-- Migrations are IN-PROCESS (no k8s Job / InitContainer).

BEGIN TRANSACTION;

ALTER TABLE auth_tokens ADD COLUMN allowed_groups TEXT DEFAULT NULL;

-- DATA REPARTITION (idempotent): move ids that name a kind='group' row
-- out of allowed_models into allowed_groups. GROUP ids and MODEL ids
-- share ONE namespace (models.id PK, 001_initial.up.sql:19-20).
-- json_valid() also guards against a malformed row aborting boot: a
-- malformed allowed_models is left untouched (the new scanner denies it).
-- json_group_array over zero rows returns '[]' on this driver (PD-4),
-- so NULLIF(..., '[]') converts an emptied column to NULL (ABSENT);
-- dialect parity with PG's json_agg (returns NULL). Both SET expressions
-- read the PRE-update row values (SQLite UPDATE semantics).
--
-- Behaviour preservation (plan §5.4): before, allowed_models=["g1"]
-- reached the group target AND its members implicitly; after the
-- repartition allowed_models=NULL + allowed_groups=["g1"] reaches
-- exactly the same set under the CONJUNCTIVE wildcard rule, and the
-- token stays RESTRICTED (never promoted to wildcard).
-- Wildcard rows (NULL allowed_models) never satisfy WHERE, so they are
-- byte-untouched. A second run matches nothing (no group id is left in
-- allowed_models) ⇒ idempotent.
UPDATE auth_tokens
SET allowed_groups = NULLIF((SELECT json_group_array(j.value)
                         FROM json_each(auth_tokens.allowed_models) j
                        WHERE j.value IN (SELECT id FROM models WHERE kind = 'group')), '[]'),
    allowed_models = NULLIF((SELECT json_group_array(j.value)
                         FROM json_each(auth_tokens.allowed_models) j
                        WHERE j.value NOT IN (SELECT id FROM models WHERE kind = 'group')), '[]')
WHERE allowed_models IS NOT NULL
  AND allowed_models NOT IN ('', '[]')
  AND json_valid(allowed_models)
  AND EXISTS (SELECT 1 FROM json_each(auth_tokens.allowed_models) j
               WHERE j.value IN (SELECT id FROM models WHERE kind = 'group'));

COMMIT;
