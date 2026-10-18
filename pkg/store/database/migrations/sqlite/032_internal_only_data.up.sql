-- Migration 032 (up, SQLite): remove-external-upstream data convergence.
-- Decision (b) = backfill-if-fully-configurable + disable-rest + report
-- (leader-confirmed; auto-convert REJECTED — see decisions.md).
-- internal semantics history: internal=0 meant "forward raw name to the
-- GLOBAL upstream_url" (no per-row URL; credentials only tolerated for the
-- ultimate-external D3 probe). That forward path is removed; rows must
-- converge to internal=1 or be disabled.
--
-- TRANSACTION WRAPPER (R10, 028 precedent): modernc.org/sqlite does NOT
-- auto-rollback partial multi-statement Execs — wrap explicitly. This is
-- HYGIENE, NOT SURVIVAL: 032 is idempotent (both arms re-match nothing on
-- re-run), a crash between ExecContext and recordMigration self-heals at
-- next boot, and partial application is behaviorally safe post-P1
-- (residual internal=0 rows hit the 404 gate).
--
-- BOOT ORDER (R10): 032 runs inside database.Initialize BEFORE the
-- managers exist and BEFORE synchronous cache priming (cmd/main.go:86 ->
-- :143-152, priming fail-fast) — zero staleness, no cross-process window,
-- no k8s migrate Job/InitContainer exists (migrations are in-process).

BEGIN TRANSACTION;

-- 1. Backfill ONLY rows that are FULLY internal-configurable:
--    credentials present AND internal_model present. Rows with credentials
--    but no internal_model were D3-probe shims (never chat-routable); rows
--    with no credentials would resolve to nothing at runtime (raw SQL
--    bypasses Go validation — the UPDATE would SUCCEED and yield enabled
--    rows whose ResolveInternalConfig/WithAffinity and the cache check all
--    return ok=false).
UPDATE models SET internal = 1
WHERE (internal IS NULL OR internal = 0)
  AND credentials_json IS NOT NULL AND credentials_json != '[]'
  AND COALESCE(internal_model, '') != '';

-- 2. Disable whatever remains non-internal (reversible; NOT delete).
--    The disable arm does NOT flip internal — the phase1 404 gate's
--    !Internal conjunct is the permanent 404 mechanism for these rows.
UPDATE models SET enabled = 0
WHERE (internal IS NULL OR internal = 0);

COMMIT;
