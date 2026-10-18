-- Migration 032 (up, Postgres): remove-external-upstream data convergence.
-- Decision (b) = backfill-if-fully-configurable + disable-rest + report
-- (leader-confirmed; auto-convert REJECTED — see decisions.md).
-- internal semantics history: internal=false meant "forward raw name to
-- the GLOBAL upstream_url" (no per-row URL; credentials only tolerated
-- for the ultimate-external D3 probe). That forward path is removed; rows
-- must converge to internal=true or be disabled.
--
-- DIALECT SPLITS (R4): `enabled` is BOOLEAN NOT NULL DEFAULT true on PG
-- (INTEGER on SQLite, migration 001) — an unswapped `enabled = 0` would
-- hard-fail PG with "column is of type boolean but expression is of type
-- integer". `internal` is ALSO dialect-split (INTEGER on SQLite, BOOLEAN
-- on PG, migration 003). Both splits are applied below; the boolean
-- column guards (`internal IS NULL OR internal = FALSE`) are defensive
-- (003 is NOT NULL DEFAULT false — NULL never occurs in practice on PG,
-- but the predicate mirrors the SQLite form for symmetry).
--
-- TRANSACTION WRAPPER (R10, 028 precedent): modernc.org/sqlite does NOT
-- auto-rollback partial multi-statement Execs, and pgx/v5 likewise
-- requires explicit BEGIN/COMMIT for multi-statement atomicity in
-- ExecContext — wrap explicitly. Idempotent (both arms re-match nothing
-- on re-run); a crash between ExecContext and recordMigration self-heals
-- at next boot; partial application is behaviorally safe post-P1
-- (residual internal=false rows hit the 404 gate).
--
-- BOOT ORDER (R10): 032 runs inside database.Initialize BEFORE the
-- managers exist and BEFORE synchronous cache priming (cmd/main.go:86 ->
-- :143-152, priming fail-fast) — zero staleness, no cross-process window,
-- no k8s migrate Job/InitContainer exists (migrations are in-process).

BEGIN TRANSACTION;

UPDATE models SET internal = TRUE
WHERE (internal IS NULL OR internal = FALSE)
  AND credentials_json IS NOT NULL AND credentials_json != '[]'
  AND COALESCE(internal_model, '') != '';

UPDATE models SET enabled = FALSE
WHERE (internal IS NULL OR internal = FALSE);

COMMIT;