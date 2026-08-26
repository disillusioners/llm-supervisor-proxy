# Phase-1 (model-credential LB) Verification Notes — 2026-08-25

Context: Phase-1 exit-gate run @ 564b64e (branch feature/model-credential-load-balancing).
Full report: RESULTS/2026-08-25-phase1-exit-gate.md. All gates PASS.

## 1. PG-gated test env recipe (reusable)
- Local PG connection lives in git-ignored `.vscode/.env.dev` as `DATABASE_URL`
  (`postgres://llm_proxy@localhost:5432/llm_proxy?sslmode=disable`, trust auth, no password).
- To run PG-gated tests: `timeout 300 env TEST_DATABASE_URL='<'DATABASE_URL value>' go test -tags pg_integration -count=1 -timeout 240s -run '<Test>' ./pkg/store/database/ -v`
- Verify non-skip: no `t.Skip` in output. PG reachable precheck: `pg_isready` / `nc -z localhost 5432` (read-only).

## 2. Anomalies surfaced (reported, not fixed — testing-only mandate)
- `pkg/models/testrefs.go` is a NON-`_test.go` file → `TestRefs`/`TestRefsWeighted` compile into the
  PRODUCTION package. Plan Task 9 said `testrefs_test.go`, but cross-package test fixtures
  (`models.TestRefs(...)` in pkg/proxy, pkg/ultimatemodel, etc.) cannot import from another package's
  `_test.go` — the placement is functionally forced; needs either a documented acceptance or an
  `internal/testrefs` sub-package. Leader ruling requested.
- `[PEAK-DBG]` debug logging left in `ResolveInternalConfig` (pkg/models) — noisy in every peak test run.
  Pre-merge cleanup candidate.
- PACKS.md count drift found & corrected: token 14→23 funcs, loopdetection 31→33, store 77→87+4.
  Lesson: refresh counts from actual `-v` output, not history, when reporting deltas.

## 3. Contract-vs-test trap avoided
- ValidationMatrix case "non-internal model with non-empty Credentials" asserts ALLOWED, while plan
  Task 6 row text still says "reject". The plan's own Amendment Changelog (Round 3d, phase1-plan.md:211)
  records the leader ruling flipping this to expected-success (D3 probe requirement; decisions.md:532).
  → When a test contradicts plan task text, check the amendment changelog BEFORE flagging a bug.

## 4. Worker incident (ops)
- Worker bfbf89be hit terminal `invalid_data / No generations found in stream` AFTER delivering its
  first task (PG gate) and BEFORE its second (spot-shadow). Revive rejected; one replacement
  (141e507c) completed the task PASS. Escape-valve (1 re-dispatch max) worked as designed.

## 5. PG gate is ONE-SHOT per database lifetime (f5f3261)
- `TestMigration028_PGFileLevelTransaction` inserts fixed-PK sentinel `pg-atomic-sentinel`
  (migrations_pg_test.go:62-67) with NO cleanup and NO ON CONFLICT → second run against the same DB
  deterministically FAILs with models_pkey 23505 at :66.
- Repro seen: 564b64e PASS left the row → f5f3261 re-run FAIL. Verified non-regression (failure
  occurs AFTER RunMigrations + columnExists assertions pass).
- Interim protocol: pre-clean `DELETE FROM models WHERE id='pg-atomic-sentinel'` (verify exactly 1
  row first) before re-running the gate. Permanent fix needs leader authorization (testing-only
  blocked it): `ON CONFLICT (id) DO NOTHING` or pre-DELETE + `t.Cleanup`.
- General lesson: shared-DB integration tests MUST use idempotent fixtures (unique/random IDs or
  ON CONFLICT + t.Cleanup) — a "green" test that seeds permanent state silently breaks its own
  next run.

