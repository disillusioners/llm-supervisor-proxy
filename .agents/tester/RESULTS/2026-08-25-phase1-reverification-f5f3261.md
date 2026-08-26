# Test Report: Phase-1 Re-verification @ f5f3261 (final close)

Date: 2026-08-25/26 UTC
Branch: feature/model-credential-load-balancing @ f5f32617e625e06a377e3707b2470d2cc56821c3 (= expected f5f3261)
Delta since 564b64e: single commit "fix(models): Phase 1 hardening — W3 escape-safe 028 backfill, W4 store-path validation, review hygiene"
Workers: 141e507c (PG gate + sentinel teardown/re-run) · efdf779c (targeted SQLite set) · 4444db92 (HEAD + PEAK-DBG grep)
Mode: TESTING ONLY — no code modified, no commits.

## Verdict: ALL THREE TASKS SATISFIED — Phase-1 final close CONFIRMED

### 1. PG gate — ✅ PASS (7/7, non-vacuous, no skips)
Command: `timeout 300 env TEST_DATABASE_URL='postgres://llm_proxy@localhost:5432/llm_proxy?sslmode=disable' go test -tags pg_integration -count=1 -timeout 240s -run 'TestMigration028' ./pkg/store/database/ -v`
- PASS: PGFileLevelTransaction · ForwardFreshDB · Backfill · **EscapeSafeBackfill (NEW)** · LosslessDown · FileLevelTransactionSQLite · **FileLevelTransactionSQLite_Rollback (NEW)**
- Both new tests confirmed present AND passing.

**Incident (resolved, non-regression):** first re-run FAILed at migrations_pg_test.go:66 — `models_pkey` duplicate on fixed-PK sentinel `pg-atomic-sentinel` (SQLSTATE 23505). Root cause: the test inserts a fixed-PK sentinel with NO cleanup and NO ON CONFLICT → one-shot per DB lifetime; the 2026-08-25 564b64e PASS had left the row behind. Failure occurred AFTER RunMigrations + both columnExists assertions passed (schema verification itself was green even in the failing run). Authorized scoped teardown performed: SELECT verify (1 row, created_at matched earlier session) → `DELETE FROM models WHERE id='pg-atomic-sentinel'` (DELETE 1) → re-run PASS.
- 🟠 STANDING ITEM (reported, not fixed): the PASS re-inserted the sentinel (created_at 05:15:58+07 verified read-only). Next re-run against this DB will deterministically FAIL again. Fix when authorized: `INSERT ... ON CONFLICT (id) DO NOTHING` or pre-DELETE + `t.Cleanup`.

### 2. Targeted Phase-1 set (SQLite) — ✅ PASS (14/14 top-level)
Command: `timeout 300 go test -run 'TestMigration028|TestModelsCredentials' -count=1 -timeout 240s ./pkg/store/database/ ./pkg/models/ -v`
- 6 migration tests (incl. new EscapeSafeBackfill + Rollback companion) + 8 TestModelsCredentials_* (CRUD_RoundTrip, ValidationMatrix 8/8, ValidationMatrix_DB 9/9, InUseGuard, both GoldenTuples, M1ShadowWriteContract).
- Reconciliation vs leader expectation: nothing missing; new-vs-564b64e = exactly the three W3/W5/W7-S1 additions. DB matrix's extra 9th case = `17_refs` (as expected).
- Note: pkg/models matched 0 (all matched tests live in store/database; run non-vacuous via that package).

### 3. [PEAK-DBG] grep — chore NOT landed at f5f3261 (reported per instruction)
- 15 hits: pkg/models/config.go ×10 (all in ResolveInternalConfig, lines 706-769), pkg/proxy/race_coordinator.go ×2 (171,229), pkg/proxy/race_executor.go ×3 (80,87,144).
- No `PEAK_DBG` underscore variant; cmd/ and test/ clean.
- ⚠️ Scope note: 5 of 15 hits are in proxy files — outside the chore's stated scope ("ResolveInternalConfig"); the chore as described would leave those behind. Not a blocker (all checks passed; leader said ignore HEAD drift unless checks fail).

## Documentation Updated
- [x] RESULTS/2026-08-25-phase1-reverification-f5f3261.md (this file)
- [x] LESSONS/2026-08-25-phase1-verification-notes.md — §5 sentinel one-shot gate
- [x] README.md — history row
- Code changes: NONE.
