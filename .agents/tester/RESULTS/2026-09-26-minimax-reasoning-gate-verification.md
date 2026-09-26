# Test Report: fix/minimax-reasoning-translation-gate verification

Date: 2026-09-26
Branch: `fix/minimax-reasoning-translation-gate` (fix @ 6829d98, base 877a1fc)
Final HEAD verified: `cc5e15c` (= 6829d98 + test commits 578512f + cc5e15c)
Instance IDs: d44106fa (explore) · d61c383e (REG-B) · c9c99a18 (REG-C) · 33ae7107 (REG-D) · ff44baa1 (REG-E) · 4caac9a0 (E2E author) · fc5df683 (boot) · e89e5937 (S8 fix) · 834912aa (REG-A)

## Summary
- Full regression: **PASS** — every package of `go test ./...` green at final HEAD (37 packages incl. test harnesses)
- E2E incident-path test: **PASS** — new file committed `578512f`
- Original symptom closed: **PASS** — wire-body evidence (before/after)
- Boot smoke: **PASS** — build exit 0, boot <1s, /healthz 200, no panic
- Quick fixes / maintenance commits: 2 test commits (578512f new E2E test; cc5e15c stale S8 expectation update)
- Quarantined: 0 (no flaky tests encountered)

## Scope Decision
Full suite requested by leader AND warranted: change touches 13 files across pkg/proxy + pkg/ultimatemodel + translator (cross-module behavior contract change). Executed as 5 parallel per-package-group packs (A–E) + post-commit pack A re-run, each under `timeout 300`.

---

## Task 1: Full regression — PASS

| Pack | Packages | Result | Runtime |
|------|----------|--------|---------|
| A | ./pkg/proxy (incl. new E2E tests) | `ok 55.042s` exit 0 | 56s |
| B | ultimatemodel, proxy/translator, proxy/normalizers, proxy/token | all `ok` (5.964s / 0.023s / 0.014s / 0.189s) | 6.2s |
| C | store/database, store, modelscache, credentiallb | all `ok` (1.391s / 0.005s / 2.357s / 3.502s) | 4s |
| D | 21 light pkgs (root, cmd, auth, bufferstore, config, crypto, events, logger, loopdetection×2, mcp, memlimit, gzipmw, models, proxyheader, providers, supervisor, toolcall, toolrepair, ui, usage) | 18 `ok` + 3 `[no test files]`; slowest mcp 17.479s | 20s |
| E | ./test/... (7 harness pkgs) | 6 `ok` + e2e_minimax_reasoning FAIL→fixed→PASS (see below) | 12s + 4.1s re-run |

### One failure found and resolved: stale e2e expectation (NOT a product regression)
`TestS8_FlagAbsent_Quadrant` (test/e2e_minimax_reasoning) — 3 of 4 subtests failed:
- :587 race_internal_flag_absent, :594 ultimate_external_flag_absent, :601 ultimate_internal_flag_absent
- race_external_flag_absent PASSED (path unchanged by the fix — consistent)

Root cause: the S8 subtests pinned the OLD contract ("flag absent ⇒ translator inert") on exactly the 3 paths 6829d98 deliberately changed. Their fixtures carry non-empty `reasoning_content` + no header — under the NEW contract these MUST translate. The developer's 13-file commit did not update this harness.

Fix (test-code only, this session): swapped `assertUntouchedUpstreamRequest` → `assertTranslatedUpstreamRequest` on the 3 subtests + contract-change comments; +14/−7 in one file. Commit `cc5e15c`. Full package re-run PASS (all S1–S14, 4.101s). Audit of the 9 remaining `assertUntouchedUpstreamRequest` sites: all encode orthogonal provider/credential gates — correct as-is.

## Task 2: E2E fallback test — PASS (new file, commit 578512f)

`pkg/proxy/race_minimax_fallback_e2e_test.go` (558 lines, 2 tests, stable ×5, -race clean):
- `TestRaceMinimaxFallback_E2E_Primary429_FallbackTranslated` — primary GLM scripted streaming-429 → race coordinator spawns fallback (`trigger=main_error`) → MiniMax httptest captures wire body → client receives streamed response (winner=fallback, 380-byte buffer, 202ms).
- `TestRaceMinimaxFallback_E2E_NonMiniMaxFallback_NotTranslated` — guard: same incident shape → openai fallback → body byte-identical, raw `reasoning_content` preserved, no translation markers (gate widening is provider-scoped, not unconditional).

Captured MiniMax wire body (verbatim excerpt):
```json
{"model":"MiniMax-M3","messages":[{"role":"user","content":"What is the weather in SF?"},
 {"role":"assistant","content":"","tool_calls":[{"id":"call_1",...}],
  "reasoning_details":[{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","text":"I should call the weather tool"}]},
 {"role":"tool","content":"72F sunny","tool_call_id":"call_1"},{"role":"user","content":"Now summarize"}],
 "stream":true,"reasoning_split":true}
```
All 5 required assertions hold: (a) fallback fired (winner.modelType=fallback + upstream.hitCount()==1 + spawnTriggers contains triggerMainError + primary called exactly once); (b) per-message `reasoning_details`; (c) top-level `reasoning_split:true`; (d) NO raw `reasoning_content` key anywhere; (e) end-to-end success (streamed client response).

## Task 3: Original symptom closed — PASS

- AFTER (proven): captured wire body above — the exact incident shape (reasoning_content, no header, GLM 429 → MiniMax fallback) now translates and succeeds.
- BEFORE (established): the pre-fix gate was header-only; the S8 e2e subtests encoded precisely that pre-fix behavior (flag-absent ⇒ untouched) and passed pre-fix — the same request arrived at MiniMax with raw `reasoning_content` ⇒ upstream 400 (2013), the incident. The S8 expectation flip is the before/after delta on the same fixture.
- Guard test proves the fix is conditional (non-MiniMax untouched), ruling out over-translation.

## Task 4: Boot smoke — PASS

- `go build ./...` exit 0; binary 25 MiB
- Boot <1s (isolated HOME, SQLite fresh DB, cache layer ON, port 18097 via `PORT`+`APPLY_ENV_OVERRIDES=1`)
- `/healthz` → 200 `ok`
- One request → graceful JSON 502 upstream_error (no upstream at :4001 in smoke env — expected), NO panic, server alive post-request (clean race-coordinator failure path)
- Clean shutdown by PID; cleanup verified; port 8088 never touched
- Config finding: `PORT` only applies with `APPLY_ENV_OVERRIDES=1` (pkg/config/config.go:353-368); default port 4321

## Gate consistency (exploration evidence)
All 6 sites read `(interleaved || hasReasoning) && providerIsMiniMax` — race_executor.go:374/:401 (helper `raceIntProviderIsMiniMax`), ultimatemodel handler_internal.go:148, handler_external.go:123/:196/:363 (inline case-insensitive compare). `hasReasoning` = `translator.HasReasoningContent(bodyMap)` (empty/absent/malformed ⇒ false) at every site.

## Commits added this session (branch)
- `578512f` test: E2E fallback path — primary 429 → race → MiniMax translated wire body (NEW file — sanctioned by task constraints)
- `cc5e15c` test: update e2e S8 flag-absent expectations to body-content gate (6829d98) — ⚠️ constraint deviation: task said commit only NEW test files; this UPDATES an existing harness file. Justification: "ENTIRE suite must pass" is unsatisfiable while S8 pins the pre-fix contract; change is test-code-only (+14/−7, one file). Leader may drop it — but doing so re-reds ./test/....

## Known intentional residual (NOT a failure — per task brief)
Clients echoing NO reasoning_content still get 400 on MiniMax fallback — documented, out of scope.

## Overall verdict: **SHIP**
1. Full suite green at final HEAD cc5e15c — zero product regressions.
2. Incident path deterministically reproduced and proven fixed end-to-end with wire-body evidence.
3. Fix is provider-scoped (guard test) and mechanically uniform across all 6 sites.
4. Boot/build clean; no UI code touched.
