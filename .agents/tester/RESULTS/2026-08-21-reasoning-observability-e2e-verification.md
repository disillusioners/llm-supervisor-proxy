# Test Report: E2E Verification — Reasoning Observability Fix (`fix/ui-reasoning-observability` @ db7aca0)

Date: 2026-08-21
Branch: `fix/ui-reasoning-observability` (base `fea5874` + fix commits 8185c76..db7aca0; verification added test-only commits `2a3cf7e`, `355f06c`, `ec1efb6`, `2317a59`, `8700a06`, `63b7701`, `1d509f8`, `b18de9a`, `fc50d7b`)
Worker instances: recon 3cfe5821 · t1/t2 398fd8ce · 4a-retry 7ac2c1e7 · 4b 77ce4b3a · 4c 5218ee54 · 4d 055451c2 · 4e 99590f95 · 4f 037e0057 · 4g 1b17323b · reg1-4 84545698/37ef01dd/6ecc9c90/e132ec9f · reg5-8 09f108f2/7dea3466/f8541dd9/173e7ce0 · reg9 87c35033 · reg10 92556694 · live c5080e39 · build 4f54fa0c

## Verdict: ALL TASKS PASS — ORIGINAL SYMPTOM CLOSED

**Original symptom:** glm-5.3 non-streaming response with `choices[0].message.reasoning_content` visible in raw server JSON produced ZERO `thinking` in the FE API (`GET /fe/api/requests/{id}`) and no 🧠 block in the Web UI.
**Status:** ✅ GONE — proven in-process (mock upstream, byte-exact), on all 10 capture paths, with translation variants, on the live model, and with client-wire byte-identity preserved.

Key recon finding: `pkg/ui/server.go` was NEVER broken (not in the branch diff) — the FE API always serialized the full RequestLog; the bug was entirely capture-side (store.Message.Thinking never populated on non-stream/request-side/ultimate/anthropic-internal paths).

## Task Results

### Task 1+3 — Reproduce original scenario + request-side: PASS (closure gate)
- New pack `test/e2e_fe_reasoning_observability/` (commits `2a3cf7e`, `2317a59`; docs `355f06c`, `8700a06`)
- **A (closure gate):** glm-5.3-shape non-stream via mock upstream → FE API over real HTTP: `messages[last].thinking` == 217-byte reasoning constant **byte-exact**; payload `"thinking"` occurrences: 1 (symptom was ZERO); belt-and-braces `reqStore.Get(id)` match.
- **B (request-side):** client-sent `messages[1].reasoning_content` → FE `thinking` byte-exact (99B); response-side thinking cleanly absent.
- **C (negative):** no-reasoning upstream → ZERO `thinking` occurrences (omitempty clean).
- **D (UI contract):** `RequestDetail.tsx` 🧠 block guards on + renders `message.thinking` — matches FE API field exactly.

### Task 2 — Path matrix (FE API thinking per path): PASS 16/16
Rows R1–R10: race-ext/int × stream/non-stream, ultim-ext/int × stream/non-stream, anthropic /v1/messages internal-stream + external-non-stream — ALL byte-exact thinking capture. N1–N4: no-reasoning → zero `thinking` occurrences on all 4 path families. M1–M2: MiniMax-credentialed flag-ON translated `reasoning_details` → FE thinking == translated text (race-ext stream 25B, ultim-ext non-stream 21B).
One harness-only fix (M2): ultimate-external MiniMax gate keys on the MODEL's CredentialID — rewired to a minimax-credentialed ultimate model; no assertion weakened; no product gap found.

### Task 4 — Wire byte-identity (the constraint): PASS across the corpus
| Corpus pack | Result |
|---|---|
| minimax_interleaved_matrix (P3-2) | PASS 34/34 test funcs (real RE2 alternation; registered `\|` form was a VACUOUS pass — repaired, see LESSONS) |
| e2e_minimax_reasoning (P3-5) | PASS 43/43; drift delta 0; S14 hygiene 0 leaks |
| e2e_ultimate_internal_reasoning | PASS (negative-control suite green through capture rewrite) |
| ultimate_model_shell_mock | PASS 48/48 (Test 8 clean) |
| minimax_reasoning_shell_mock | PASS 53/53 (T3b fix still green) |
| openai_internal_buffered_shell_mock | PASS 60/60 |
| **anthropic leak spot-check** (new pack `test/e2e_anthropic_thinking_leak/`, commit `63b7701`) | PASS 3/3 — internal stream: ZERO thinking bytes on client wire while sink captures 51B byte-exact; external stream: thinking_delta present (no over-swallow); internal non-stream: wire byte-identical at parent `effc345` (translated block pre-existing from translator/response.go). **Mutation-proven non-vacuous**: re-injecting the leak in a worktree makes S1 FAIL with 4 detections. |

### Task 5 — Full regression (`go test ./pkg/...` equivalent via registered packs): PASS 10/10
proxy 376 · ultimatemodel 142 · store 77(+4 PG-gated skips) · models 87 · toolrepair 17/105 · loopdetection 31 · auth 48 · token 14 · mcp green (3 intentional SSRF/DNS skips; pack script materialized from "(or inline)" registration — commits `b18de9a`+`fc50d7b`) · misc 348. Zero failures, zero regressions of db7aca0, zero unclassified issues. pkg/mcp confirmed green (brief's "reportedly green" verified).

### ensure.md Criticals: 4/4 PASS
go vet clean · go build clean · frontend npm build clean (vite, 0 errors) · all Go unit tests green via the 10 packs above.
**Improvement notice (user-owned file, not modified):** `npm run build` = `vite build` only — no TS type-check step; if intent is "no TS type errors", add `vue-tsc --noEmit` (see RESULTS/2026-08-21-ensure-validation.md).

### Live smoke (user-directed, post-suite): PASS — 2/2 live calls
Real glm-5 family (zai credential; upstream served **glm-5.3** — the exact original-symptom model), 1 non-stream + 1 stream, zero MiniMax usage (fallback-to-`smart` chain explicitly disabled via env guard; verified `fallback: not_started` on both calls).
- Non-stream: FE thinking len=529 == wire reasoning_content 1:1; content 360B; `"thinking"` occurrences 1 (was ZERO pre-fix).
- Stream: FE thinking len=56 byte-identical to reassembled wire deltas.
- Cleanup verified: token deleted (204), proxy killed by PID, port 4399 freed, user's debug proxy untouched.

## Scope Decision
Full-suite-level run WAS warranted: closure gate on a cross-module capture-layer rewrite (pkg/proxy + pkg/ultimatemodel, 20 files, +3472/−78) + explicit full-regression task. Executed as 13 parallel pack dispatches + 2 new-pack builders + 1 live smoke, all ≤5-min dual-layer timeout.

## Operational notes
- One worker (4a first attempt) died at instance level pre-report; ONE replacement dispatched per policy — completed cleanly.
- All test artifacts committed with `test:`/`docs(tester):` prefixes; working tree had only intentional `.agents/tester/` modifications at close.

## Overall Status
- Task 1 (closure gate): ✅ PASS
- Task 2 (path matrix): ✅ PASS 16/16
- Task 3 (request-side): ✅ PASS
- Task 4 (byte-identity): ✅ PASS (corpus 6 packs + new anthropic spot-check, mutation-proven)
- Task 5 (full regression): ✅ PASS 10/10 packs
- ensure.md: ✅ 4/4 Critical
- Live confirmation: ✅ PASS (glm-5.3, stream+non-stream)
- **Testing Complete: ✅ READY — symptom closed, constraint held, zero regressions**
