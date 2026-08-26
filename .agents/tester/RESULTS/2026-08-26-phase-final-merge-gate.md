# Test Report: FINAL Merge Gate — model-credential load balancing (Phases 1–5)

Date: 2026-08-26/27 (UTC)
Branch: feature/model-credential-load-balancing @ d6368bd79c668843587aa5ef7efb46c5b4b69d0e (verified by all 17 workers; exact match, no +docs on top at run time — dev later committed docs, branch now 3 ahead of origin, tree clean)
Workers: 4444db92 (build/vet) · 141e507c (race) · 82a58253 (PG×2) · 548a7fd3 (E2E) · 419b2e68 (frontend + artifact cleanup) · 093cbf35 (cross-pkg) · 10 pack workers (d8636363, a5f6479d, efdf779c, b24e8fa5, 3f2a174a, c55fd42a, 8e24ba7b, 6dc67fa9, dd1a922c, 2f6c2112)
Mode: TESTING ONLY — zero code modifications, zero commits; two test-tooling artifacts (pnpm lock/workspace stubs) created then removed; worktree left clean.

## Verdict: ✅ ALL 7 GATES GREEN — FEATURE VERIFIED, MERGE PREP UNBLOCKED

### 1. Full Go suite — ✅ 0 failures
`go build ./...` exit 0 (2s) · `go vet ./...` exit 0 (1s) · 10 packs = full package set:

| Pack | Result | Counts | vs Phase-1 baseline / last gate |
|---|---|---|---|
| proxy | PASS | 397 top + 372 sub / 7 skips | +21/+2 vs 376/370 — growth attributed: handler_lb_phase5_test.go (LB wiring + failover tests; `[LB-FAILOVER]` telemetry is expected) |
| ultimatemodel | PASS | 145/145 | +3 (TestUltimateInternal_{NonRateLimit_Propagates, SingleCredential_Propagates, RateLimitFailover_SucceedsOnSecondCredential}) |
| store | PASS | 99 + 4 PG-skips | flat vs @315f4e8 (0 add/remove) |
| models | PASS | 87 + 267 sub | flat; PEAK-DBG still gone |
| misc | PASS | 275 top + 119 sub | +17/+29 — incl. 11 named pkg/ui multi-credential DTO tests (POST accepts/stamps positions, rejects dup/zero-weight/mixed-provider/unknown/exceeding-max/empty-when-internal, legacy credential_id ignored, GET returns array, PUT 1→2 refs) |
| auth | PASS | 48/48 | flat |
| mcp | PASS | 245/474/231 | identical (3-gate trail: 564b64e/315f4e8/d6368bd all 245-0-0) |
| token | PASS | 23 + 118 sub | flat (3-gate stable) |
| toolrepair | PASS | 17/105 | flat (3-gate identical) |
| loopdetection | PASS | 33/33 | flat (3-gate identical; last touching commit pre-dates feature) |

**Zero shrinks anywhere.** All growth is named feature-test additions.

### 2. Merge-gate race set — ✅ PASS, RACE: CLEAN
`-race -count=1` over credentiallb+proxy(+normalizers/token/translator)+ultimatemodel+store/database+ui: 9 package lines all `ok` (33s), zero `DATA RACE`.

### 3. PG gate ×2 — ✅ DOUBLE-RUN SAFE: YES
7/7 tests PASS on RUN1 AND RUN2 (identical matched set, no duplicate-key errors, no manual DB writes). The dev's conflict-safe sentinel fix holds — the Phase-1 one-shot issue is closed.

### 4. E2E — ✅ 6/6 first attempt (91s, exit 0, no flake, no re-run needed)
Full verbatim paste preserved below + `/tmp/finalgate_e2e_paste.log`. Mock ports 4001/4002/4003 + proxy 4322 (harness-fixed convention); script self-cleans.

### 5. Frontend — ✅ BUILD PASS · TYPECHECK 30/30 (NEW=0) · no test script exists
- Repo is **npm-managed** (package-lock.json; no pnpm lockfile) — worker adapted to npm per task allowance. `npm run build` (vite) exit 0 in 1.09s (48 modules, 4 assets).
- `tsc --noEmit`: exactly 30 errors = the 30-error pre-existing baseline, delta 0, zero NEW (full list captured for audit).
- No test script in package.json (only dev/build/preview); no vitest/jest config — "pnpm test" not applicable.
- Note: worker's corepack/pnpm probe created `pnpm-lock.yaml`+`pnpm-workspace.yaml`; both removed in cleanup; `pkg/ui/frontend/` porcelain clean.

### 6. Cross-package regression — ✅ PASS (5 trees, 18s)
models ok 0.013s · providers ok 0.089s · cmd [no test files] (expected) · mcp ok 16.404s · auth ok 0.370s — zero failures outside the race set.

### 7. Original-symptom closure — ✅ PROVEN (E2E Test 9, quoted walk)
```
HTTP status: 200
Body: "Hello from mock LLM C" (identity marker of third credential)
Hit counts: A success=0 rate_limited=1 | B success=0 rate_limited=1 | C success=1 rate_limited=0
✓ Test 9 failover: 2× 429 + 1× 200 across 3 credentials
```
Exactly one 429 on cred-A, one 429 on cred-B, one 200 on cred-C with C's identity marker to the client — the user's core scenario (3-cred MiniMax-like failover) walks cred_pick_1→429→cred_pick_2→429→cred_pick_3→200 end-to-end, no fallback model involved.

---

## FULL E2E VERBATIM PASTE (PR-description ready — exit precondition #12)

```text
============================================
  Model-Credential LB E2E (Phase 5 / W9)
  Max runtime: 90s
============================================
  Workdir HOME: /var/folders/3m/p68dltbj1xv7hjkdxr_gqnc00000gn/T/cred_lb_e2e.XXXXXX.yaOGieh79x
  Mock ports:   4001 (A), 4002 (B), 4003 (C)
  Proxy port:   4322
Cleaning ports (proxy=4322, mock=4001)...
  Port 4322: already free
  Port 4001: already free
  mock_llm.go: not running
  mock_llm_race.go: not running
  mock_llm_loop.go: not running
  mock_llm_openai.go: not running
  cmd/main.go: not running
Port cleanup complete

Starting Mock A (port 4001, identity=A)
Mock A started (PID 81515)
Starting Mock B (port 4002, identity=B)
Mock B started (PID 81553)
Starting Mock C (port 4003, identity=C)
Mock C started (PID 81563)

Building proxy binary (cached after first run)...
Starting proxy on port 4322 ...
Proxy started (PID 82670)

Configuring credentials, tokens, and model...
  ✓ Created credentials cred-A, cred-B, cred-C
  ✓ Created 2 tokens (sk-... prefix)
  ✓ Created model 'cred-lb-model' with 3 equal-weight creds

━━━ Test 1: Affinity (same token, same message ×5) ━━━
  Hit counts:    A: success=0 rate_limited=0 | B: success=0 rate_limited=0 | C: success=5 rate_limited=0
  ✓ Test 1 affinity: 5/5 on a single identity

━━━ Test 2: Distribution (100 unique first-user messages) ━━━
  Hit counts:    A: success=34 rate_limited=0 | B: success=38 rate_limited=0 | C: success=28 rate_limited=0
  ✓ Test 2 distribution: 100 hits split across ≥2 identities (each in [10,60])

━━━ Test 2b: Templated first message, rotating tokens ━━━
  Hit counts:    A: success=0 rate_limited=0 | B: success=25 rate_limited=0 | C: success=25 rate_limited=0
  ✓ Test 2b templated: 50 hits split across 2 identities (A=0 B=25 C=25)

━━━ Test 2e: Multimodal affinity (same content ×5) ━━━
  Hit counts:    A: success=0 rate_limited=0 | B: success=0 rate_limited=0 | C: success=5 rate_limited=0
  ✓ Test 2e multimodal affinity: 5/5 on a single identity

━━━ Test 8: /v1/messages affinity (same Anthropic-shape ×5) ━━━
  Hit counts:    A: success=0 rate_limited=0 | B: success=0 rate_limited=0 | C: success=5 rate_limited=0
  ✓ Test 8 /v1/messages affinity: 5/5 on a single identity

━━━ PHASE 2: Restarting mocks with -fail-429-once=1 ━━━
  (only A/B carry -fail-429-once=1; C always 200; weights [A:1000,B:1000,C:1] bias chain order)
Restarting Mock A (port 4001, -fail-429-once=1) — ready (PID 84410)
Restarting Mock B (port 4002, -fail-429-once=1) — ready (PID 84436)
Restarting Mock C (port 4003, NO -fail-429-once) — ready (PID 84516)
Creating fresh Phase-2 credentials + model...
  ✓ Created credentials cred-A2, cred-B2, cred-C2
  ✓ Created model 'cred-lb-failover' (no fallback chain)

━━━ Test 9: Full failover chain (3 creds, no fallback model) ━━━
  HTTP status: 200
  Body (first 300 chars): {"id":"chatcmpl-mock-1787761711973866000","object":"chat.completion","created":1787761711,"model":"mock-model","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from mock LLM C"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}
  Hit counts: A success=0 rate_limited=1 | B success=0 rate_limited=1 | C success=1 rate_limited=0
  ✓ Test 9 failover: 2× 429 + 1× 200 across 3 credentials (client got identity marker)

============================================
             Test Summary
============================================
  PASS: Test 1 affinity: 5/5 on a single identity
  PASS: Test 2 distribution: 100 hits split across ≥2 identities (each in [10,60])
  PASS: Test 2b templated: 50 hits split across 2 identities (A=0 B=25 C=25)
  PASS: Test 2e multimodal affinity: 5/5 on a single identity
  PASS: Test 8 /v1/messages affinity: 5/5 on a single identity
  PASS: Test 9 failover: 2× 429 + 1× 200 across 3 credentials (client got identity marker)

  Passed: 6
  Failed: 0

All 6 scenarios passed.
Cleaning up processes and state... done
```
(Raw log incl. ANSI + mock-restart `Terminated: 15` lines: `/tmp/finalgate_e2e_paste.log` — those lines are the script's own phase-2 restarts/cleanup, expected.)

## Anomalies
- 🟢 E2E script documents a deliberate Phase-2 deviation banner (only A/B 429-once; weights bias chain order) — intentional test design, disclosed in-output.
- 🟢 pnpm artifacts created-then-removed (tooling hygiene; final tree clean).
- 🟢 Frontend typecheck baseline (30 errors) unchanged — technical debt predates feature; not introduced here.
- No flakes, no timeouts, no races, no failures anywhere in the gate.

## Code Changes: NONE

## Documentation Updated
- [x] RESULTS/2026-08-26-phase-final-merge-gate.md (this file)
- [x] PACKS.md — all packs @ d6368bd
- [x] README.md — history row
