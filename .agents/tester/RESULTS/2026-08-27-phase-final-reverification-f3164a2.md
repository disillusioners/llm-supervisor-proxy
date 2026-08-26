# Test Report: FINAL Pre-Merge Re-Verification @ f3164a2 (Phase-5 review fixes)

Date: 2026-08-27 (UTC)
Branch: feature/model-credential-load-balancing @ f3164a2291a51bda50bc94f081466d7dc444033d (parent 2c70018; verified by all 5 workers)
Workers: 548a7fd3 (E2E ×2) · 141e507c (race set) · 093cbf35 (full suite) · 9ac635c0 (W1 claim-check) · 4444db92 (HEAD/dirt)
Mode: TESTING ONLY — no code changes, no commits. Tree: only 2 expected `.agents/` planner docs dirty.

## Verdict: ✅ ALL 5 TASKS GREEN — INDEPENDENT CONFIRMATION AT f3164a2; MERGE DECISION UNBLOCKED

### 1. E2E independent ×2 — ✅ 6/6 + 6/6 (outcome (a): clean consecutive runs)
- RUN1: 91s exit 0; RUN2: 90s exit 0. No flake, no lock contention, fresh mktemp HOMEs each run.
- **Test 2b (F1 fix) verbatim, RUN1:** `Hit counts: A: success=5 | B: success=3 | C: success=2` → `✓ 10 independent picks, ≥2 identities nonzero, max ≤ 7/10 (A=5 B=3 C=2)`; RUN2 independent draw A=6/B=3/C=1 — both pass. Banner documents the hardening: 2×25 (P[all-same]≈33%) → 10×1 (P≈5.08e-5).
- **Isolation observation:** atomic-mkdir lockfile silent on success both times; run-2 preamble shows ports already free + fresh HOME → run 1's cleanup released everything. (Exit-2 contention path not exercised — no contention existed.)
- Review-fix shape deltas confirmed in-output: W4 build-before-HOME + prebuilt mock binary banners; W6 weights [1e6,1e6,1] (flake ~3e-6/run); PORT_A/B/C/PROXY env isolation; Test 9 walk unchanged (A 429 → B 429 → C 200, "Hello from mock LLM C").
- Logs: /tmp/f3164a2_e2e_run1.log, run2.log (115 lines each).

### 2. Merge-gate race set — ✅ PASS, RACE: CLEAN (suspected race NOT tripped)
9 packages `ok` (32s; proxy 31.1s), zero DATA RACE, single faithful run (no retries). Raw log /tmp/race_gate_f3164a2.log.
**Adjudication evidence:** non-reproduction of race_executor.go:526 ↔ race_request.go:255 at f3164a2 under the 5-tree merge-gate set. Caveats for the wanderer: (i) this is the merge-gate tree set, not literally `./... -race` — different concurrent-scheduling window; (ii) race-detector non-trip = evidence of non-reproduction in these runs, not proof of absence.

### 3. Full suite (no race) — ✅ PASS, 0 failures
`timeout 300 go test ./... -count=1 -timeout 280s` → 31 packages `ok` + 5 `[no test files]`, 0 FAIL, 33s wall. Includes credentiallb (3.5s), proxy (28.9s), all 6 `test/e2e_*` sub-packages. W2's 6 no-op-injection deletion orphaned nothing.

### 4. W1 spot-proof — ✅ PASS + 3/3 claims VERIFIED (mutation-sensitivity corroborated)
`TestShutdownOrder_MainGoDeferRegistrationOrder` @ pkg/proxy/handler_lb_phase5_test.go:720 — PASS with t.Log evidence: dbStore.Close@main.go:67 < modelsMgr.Close@69 < credLB.Stop@96. Claims verified from source: (1) reads real cmd/main.go via runtime.Caller(0)-resolved path (not a fixture); (2) asserts strict `<` on captured defer line numbers (not mere presence) with comment-line + multi-registration ambiguity guards; (3) mutation-sensitive — weakened presence-only/`<=`/no-comment-guard variants would each be caught by a different assertion. Note: the 4th check (`srv.Shutdown(` @259) is presence-only — weakest assertion, informational only.

### 5. Working tree — ✅ ONLY-EXPECTED dirt
2 modified files, both `.agents/shared/planning/model-credential-load-balancing/` planner docs. Zero `.go`/`.sql`/`.mod`/`.sum`/frontend dirt. (d6368bd-era README/CHANGELOG/pnpm dirt was absorbed by f3164a2/2c70018/393e520 commits — branch now clean of it.)

## For the parallel provenance adjudication
Empirical summary: the suspected race was NOT observed in (a) the 5-tree -race merge-gate set at f3164a2 (this report), nor previously at d6368bd (identical set, RACE: CLEAN). No capture exists from either gate run. The wanderer's code-level analysis remains the primary provenance path; our data = two clean non-reproductions.

## Code Changes: NONE
## Documentation Updated
- [x] RESULTS/2026-08-27-phase-final-reverification-f3164a2.md (this file)
- [x] README.md history row (appended to prior entry set)
