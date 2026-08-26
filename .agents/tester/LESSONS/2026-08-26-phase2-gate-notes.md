# Phase-2 Gate Notes — 2026-08-26 (@ 315f4e8)

Full report: RESULTS/2026-08-26-phase2-gate.md. Verdict: PASS (1 adjudication item).

## 1. W8 test-seam vs `ForTest` gate (leader ruling needed)
- Gate "ForTest in production files = 0" literal-FAILs: 13 hits, all in pkg/credentiallb/testhooks.go
  (no build tag) + doc.go quote. doc.go:82-88 documents this as the deliberate W8 pattern
  (single-file test-hook boundary; engine/binding/selector never reference hooks).
- Options: accept W8 (document the gate exception), add `//go:build` tag, or internal-package seam.
- Note: cross-package test usage exists? — Phase-2 store_test.go uses e.InjectPreconditionStateForTest
  (store/database importing credentiallb) → a plain `_test.go` relocation is NOT possible; build tag
  or internal package are the viable mechanical fixes if the literal gate is upheld.

## 2. E-3 fast-path contract wording (plan-doc reconciliation)
- Implemented+tested contract: fast path = ZERO map writes AND ZERO stats ticks
  (engine_test.go:306-323: Bindings==0, Hits==0, Misses==0).
- Gate/plan wording ("Misses pinned as selection performed, Round 3h") matches W-2 empty-key
  semantics (Misses==3000, Hits==0), not E-3. Fix the wording at plan level; code/tests consistent.

## 3. Claim-verified spot-proof pattern — regex-coverage trap
- 3 leader-supplied `-run` regexes collectively missed 5 claim-carrying tests:
  TestEngine_W2_EmptyKey_NoBinding, TestEngine_E3_FastPath_NoMapWrites (prefix mismatch),
  TestEngine_SkipCooling_RenormalizedDistribution, TestResolveInternalConfigWithAffinity_BranchTable
  (different test-family prefix). Green-on-matched-set would have silently under-covered 5 of 10 claims.
- Protocol fix: BEFORE running a claim spot-proof, map each claim → exact test name via
  `grep -n "func Test" pkg/.../*_test.go`; build the `-run` alternation FROM that map; then run.
  The claim-table step caught it every time — keep claim tables mandatory for spot-proofs.

## 4. pkg/credentiallb pack gap
- New package (binding/engine/events/key/selector + 4 test files) has no PACKS.md entry.
  Covered this session by the -race full-package run + spot-proofs. Create
  test/packs/credentiallb_unit_test.sh before Phase 3 so the registry stays complete.

## 5. Baseline drift (informational)
- store pack 87+4 → 99+4 (+9 Phase-2 TestStoreEngine_* + Affinity_BranchTable, +3 late-P1).
  All other packs byte-identical to 564b64e baseline — strongest possible no-spillover signal.
