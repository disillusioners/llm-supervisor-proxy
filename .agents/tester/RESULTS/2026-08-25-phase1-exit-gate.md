# Test Report: Phase-1 Exit Gate — model-credential load balancing

Date: 2026-08-25 (UTC)
Branch: feature/model-credential-load-balancing @ 564b64e (verified exact)
Role: TESTING ONLY — no code modified, no commits made by test workers.
Workers: 4444db92 (build/vet, golden) · bfbf89be (PG gate; crashed before spot-shadow) · 141e507c (spot-shadow replacement) · efdf779c, b24e8fa5, d8636363, a5f6479d, 3f2a174a, c55fd42a, 8e24ba7b, 6dc67fa9, dd1a922c, 2f6c2112 (packs)

## Summary — ALL EXIT CRITERIA MET

| Leader task | Result | Evidence |
|---|---|---|
| 1. MANDATORY PG gate (4b) | ✅ PASS | `TestMigration028_PGFileLevelTransaction --- PASS (0.07s)`, not skipped, exit 0 |
| 2. Standard suite | ✅ PASS | build+vet clean; 10 registered packs = full `go test ./...` package set, 0 failures |
| 3. M-1 shadow spot-check | ✅ PASS | 10/10 matched tests green (non-vacuous), contract assertions source-verified |
| 4. Golden-tuple backward compat | ✅ PASS | 2/2 tests green — exists at models_credentials_test.go:445/:486 |

**Overall: PHASE-1 EXIT GATE CLOSED — READY.**

## 1. PG Gate (task 1) — PASS
- Env source: `.vscode/.env.dev` (git-ignored), var `DATABASE_URL` → mapped to `TEST_DATABASE_URL`. URL: `postgres://llm_proxy@localhost:5432/llm_proxy?sslmode=disable` (no password component — local trust auth).
- Command: `timeout 300 env TEST_DATABASE_URL='<url>' go test -tags pg_integration -count=1 -timeout 240s -run 'TestMigration028_PGFileLevelTransaction' ./pkg/store/database/ -v`
- Output verbatim: `--- PASS: TestMigration028_PGFileLevelTransaction (0.07s)` / `ok ...pkg/store/database 0.082s`, exit 0. No `t.Skip` emitted (env reached the process).
- Proves: repo's first file-level `BEGIN…COMMIT` migration (028) commits atomically through pgx/v5 ExecContext — plan risk P1-8 MANDATORY gate.

## 2. Standard Suite (task 2) — PASS
- `go build ./...` exit 0 (2s); `go vet ./...` exit 0 (1s). HEAD = 564b64e012cc9058c997358900b1276c243ae0ef; only non-Go `.agents/` docs dirty.
- Executed as the 10 PACKS.md-registered unit packs (same package set as `go test ./...`; each dual-layer: outer `timeout 300` + script 120s/go 110s):

| Pack | Result | Counts | Runtime |
|---|---|---|---|
| proxy_unit_test | PASS | 376 top + 370 sub / 7 intentional skips (heartbeat/idle long-wait) | 26s |
| ultimatemodel_unit_test | PASS | ~90+ funcs green | 4.4s |
| store_unit_test | PASS | 87 pass / 4 PG-gated skips (+51 subtests) | 2s |
| models_unit_test | PASS | 87/87 | <1s |
| toolrepair_unit_test | PASS | 17 funcs / 105 subtests | 1s |
| loopdetection_unit_test | PASS | 33/33 | 1s |
| auth_unit_test | PASS | 48/48 | <1s |
| token_unit_test (inline) | PASS | 23 funcs / ~118 subtests | <1s |
| mcp_unit_test | PASS | ~60 funcs / 245 subtests | 16s |
| misc_unit_test | PASS | 9 pkgs, ~280 funcs (incl. pkg/ui DTO shim — P1-6 guard) | ~5s |

Zero FAIL, zero TIMEOUT across all packs. No flakes observed (single deterministic runs).
Note: store pack 87+4 vs PACKS.md prior 77+4 — delta = new Phase-1 tests (expected).

## 3. M-1 Shadow Spot-Check (task 3) — PASS (non-vacuous: 10 matches)
`timeout 300 go test -run 'TestMigration028|TestModelsCredentials' ./pkg/store/database/ ./pkg/models/... -v` → 10/10 PASS:
TestMigration028_ForwardFreshDB · _Backfill · _LosslessDown · _FileLevelTransactionSQLite · TestModelsCredentials_CRUD_RoundTrip · _ValidationMatrix (9/9 subtests) · _InUseGuard · _SingleCredentialGoldenTuple · _PeakHourGoldenTuple · _M1ShadowWriteContract.
Contract source-verified: both columns present post-028 + index intact (migrations_test.go:88,203-204); byte-exact backfill `[{"credential_id":"cred-X","weight":1,"position":0}]` (:181-186); Go-computed shadow write maintained through CRUD.
(pkg/models matched 0 — all matched tests live in pkg/store/database; run still non-vacuous.)

## 4. Golden-Tuple Backward Compat (task 4) — PASS (exists + green)
- `TestModelsCredentials_SingleCredentialGoldenTuple` @ models_credentials_test.go:445 — ResolveInternalConfig 5-tuple (provider/apiKey/baseURL/internalModel/ok) byte-identical for single-credential model.
- `TestModelsCredentials_PeakHourGoldenTuple` @ :486 — peak-hour variant resolves gpt-peak (branch fires, log-confirmed).
Both PASS (0.02s/0.01s).

## Scope Decision
Full Go suite warranted (phase-exit gate; 137-literal/32-file cross-module sweep). Scoped OUT with reason:
- Mock/E2E shell packs — plan states "no behavioral change to request routing"; not in exit criterion. Expandable on request.
- Frontend build — zero FE source files in the Phase-1 change set (files table: Go + SQL only; `ui/server.go` is Go-side, covered by misc pack).

## ensure.md Validation
- Critical 4/4 in-scope: Go tests ✅ (via packs) · vet ✅ · project build ✅ · frontend build — NOT re-run (no FE source changed; last validated state stands; flag if FE build gate is desired for phase exit).
- Important: peak-hour items covered indirectly (golden-tuple peak test + 87/87 models pack incl. peak_hours). Migration-018 item out of scope for this change.
- No contradictions between ensure.md methods and pack rules this run (all validations were pack-based).

## Anomalies / Notes (no action taken — testing-only mandate)
1. 🟠 `TestRefs` fixture factory lives at `pkg/models/testrefs.go` — a NON-`_test.go` file, so it compiles into the production package (plan Task 9 specified `testrefs_test.go`; house style puts test helpers in _test.go files). Not a test failure; recommend renaming to `testrefs_test.go`... BUT NOTE: cross-package test files import it (`Credentials: models.TestRefs(...)` in other packages' tests), which is impossible from a `_test.go` file — the current placement is likely deliberate for that reason; if so, document why. Surface to leader for a ruling.
2. 🟠 Leftover `[PEAK-DBG]` debug logging emitted by `ResolveInternalConfig` on every call (models pack output). Pre-merge cleanup candidate.
3. 🟢 PACKS.md count drift (token 14→23 funcs, loopdetection 31→33, store 77→87+4) — corrected in this update.
4. 🟢 "Non-internal + creds allowed" in ValidationMatrix initially looked like a plan/task-6 mismatch — verified as documented leader ruling Round 3d (phase1-plan.md:211, decisions.md:532; D3 probe requirement). Test is correct per amended contract.
5. 🟢 Worker bfbf89be crashed (stream error) after delivering the PG gate but before its spot-shadow task; one replacement (141e507c) re-ran it cleanly — escape valve applied exactly once, no coverage gap.

## Worker Incident Log
- bfbf89be: `invalid_data / No generations found in stream` — terminal error state; revive rejected by system; replaced once (141e507c) which completed PASS. No other incidents.

## Documentation Updated
- [x] RESULTS/2026-08-25-phase1-exit-gate.md (this file)
- [x] PACKS.md — last-run/status refreshed for 10 packs + count corrections
- [x] LESSONS/2026-08-25-phase1-verification-notes.md — anomalies 1-3 + PG env mapping
- [x] README.md — testing-history row appended
- [ ] rules/ensure.md — untouched (user-owned)
- Code changes: NONE (testing-only session honored)
