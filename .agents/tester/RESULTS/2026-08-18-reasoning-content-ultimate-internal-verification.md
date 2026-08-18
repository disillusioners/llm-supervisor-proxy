# Test Report: Verification of commit 83814b0 — reasoning_content on ultimate-internal path

**Date:** 2026-08-18
**Branch:** `feature/reasoning-content-ultimate-internal` (baseline `8233dbc`)
**Worker instances:** f5239229 (recon), 2551816a (ultimatemodel pack), fe61a6f2 (proxy pack), 0f67bbc9 (reachability), 111ec698 (static gates), 441dd647 (scenario repro), 501f4df4 (ultimate shell pack + Test 8 triage), 2d061120 (Test 8 quick fix)

## Summary
- **Original symptom: CONFIRMED GONE** (end-to-end reproduction passes with fix; fails with the exact original symptom when fix reverted; passes again on restore)
- 4 of 4 verification tasks: **PASS**
- Packs: 4 PASS + 1 PASS-after-harness-repair (failure was pre-existing harness rot, not the commit)
- Quick fixes / maintenance: 3 commits (all test-code only, zero production changes)
- Quarantined: 0

## Scope Decision
> Full regression command requested (`go test ./pkg/ultimatemodel/... ./pkg/proxy/...` + relevant mock e2e). Change touches 1 production file (`pkg/ultimatemodel/handler_internal.go`, +4 lines, single-field copy) + its test + harness scripts → ran the two packs exactly mapping the user's command (`ultimatemodel_unit_test`, `proxy_unit_test`), the two ultimate/internal-path shell mock packs, a new reproduction scenario, the reachability probe, and static gates (vet/build). Skipped: remaining 9 unit packs, `e2e_reasoning_content` pack (exercises race_executor path, not this change set), frontend build (no frontend files in commit). Full suite not warranted.

## Task 1 — Reproduce original scenario end-to-end: ✅ PASS
**Worker:** 441dd647 (`mock-test`) · **New asset:** `test/e2e_ultimate_internal_reasoning/` (commit `c3a4b35`, test-code only)

Scenario: 3-message conversation, `reasoning_content` on assistant message index 1; ultimate fallback → INTERNAL model; capturing mock upstream asserts the **forwarded** body.

| Run | State | Result |
|---|---|---|
| a | HEAD 83814b0 | **PASS** — captured upstream `messages[1].reasoning_content` == sent value |
| b | fix reverted | **FAIL** — `has-field=false, got="", want="Let me reason deeply…"`; captured `messages[1]` only role/content (**the exact original symptom**) |
| c | fix restored | **PASS** — byte-identical to run a; `handler_internal.go` MD5 == blob at 83814b0 |

Trigger: `X-Force-Ultimate-Model: true` admin header (`pkg/proxy/handler.go:464-469`, fail-closed) + `ULTIMATE_MODEL_MAX_RETRIES=0`. Assertions slot-precise: index 1, exact value, 3-message count, all `content` intact, `model` rewritten to ultimate InternalModel.

Captured upstream body (run a): `messages[1] = {"content":"The answer is 4.","reasoning_content":"Let me reason deeply about whether 2+2=4...","role":"assistant"}`.

## Task 2 — Regression checks: ✅ PASS
| Pack | Result | Detail |
|---|---|---|
| `ultimatemodel_unit_test` (`timeout 300` outer / 120s inner) | **PASS** 6s | 100+ tests green; `TestConvertRequest_ReasoningContent` (commit's new regression test, 4 cases) passed; both historically flaky tests (`TestExecute_PerTokenOverride_WithID`, `TestExecute_AllLookupsUseID/per-token_uses_ID_lookup`) now pass |
| `proxy_unit_test` | **PASS** 25s | 0 failures; historical stream_buffer flaky did not recur |
| `test_mock_ultimate_model.sh` (48 checks) | **PASS 48/48** 34s (after harness repair) | All reasoning-content checks green on every run |
| `test_mock_openai_internal_buffered.sh` (60 checks) | **PASS 60/60** 15s | Clean |

Static gates: `go vet ./...` exit 0; `go build ./cmd/main.go` exit 0.

### Incidental finding (NOT a regression of 83814b0)
`test_mock_ultimate_model.sh` Test 8 (retry-limit error propagation) failed deterministically 3× — triaged **pre-existing** (added `f3e4205` 2026-03-18; `git diff 83814b0^ 83814b0 -- <script>` is **empty**). Root cause: Test 8 curls used `--max-time 5` while `MAX_GENERATION_TIME=20s`; curl hung up at 5s → `context canceled` → empty error body. Not flaky (0/3 pass) → no quarantine. Fixed as harness maintenance (below); pack green 48/48 after.

## Task 3 — Reachability probe, third converter: VERDICT = REACHABLE-UNDER-CONDITION, bug DORMANT
**Worker:** 0f67bbc9 (read-only, no changes)

`InternalHandler.convertRequest` (`pkg/proxy/internal_handler.go:272`, rebuild loop `:282-349` — copies role/content/name/tool_call_id/tool_calls, **no reasoning_content**, 0 occurrences of "reasoning" in file) is live on exactly ONE route:

```
POST /v1/messages                          cmd/main.go:139
→ HandleAnthropicMessages                  handler_anthropic.go:39
→ gate: modelConfig.Internal               handler_anthropic.go:282
→ gate: credential provider != "anthropic" handler_anthropic.go:300-339
→ doAnthropicInternalRequest               handler_anthropic.go:340
→ HandleRequest → convertRequest           handler_anthropic.go:461-462 → internal_handler.go:95 → :272
```

**But client `reasoning_content` cannot reach it**: (1) Anthropic request types (`translator/types.go:15-40`) have no such field — dropped at unmarshal; (2) `thinking` blocks explicitly discarded in translation (`translator/request.go:554-556`). `/v1/chat/completions` does NOT use this converter (internal models route via `race_executor.go:655`, which preserves reasoning_content at `:724`). **No live loss path today.** Latent risk: becomes live if the translator ever maps thinking→reasoning_content forward (reverse direction already exists at `translator/response.go:117-133`); Anthropic internal branch is also untested. Recommended (not done, per instruction): parity fix + coverage in a follow-up change.

## ensure.md Validation Results (scoped)
- **Critical**
  - ✅ All Go unit tests pass for changed packs — `ultimatemodel_unit_test` + `proxy_unit_test` PASS (my scoped pack equivalent of the literal `go test ./...`)
  - ✅ `go vet ./...` no issues — exit 0
  - ✅ Full project builds — `go build ./cmd/main.go` exit 0
  - ✅ (Frontend build — not run: no frontend files in change set; not in scope for this commit)
- **Important / Nice-to-have**: peak-hour items out of scope for this change set (no peak-hour files touched) — not validated this run.
- No contradictions with ensure.md methods this run (validations were executed as packs with dual-layer timeouts).

## Quick Fixes / Maintenance Applied (all test-code only)
| Commit | What |
|---|---|
| `c3a4b35` | NEW e2e reproduction test `test/e2e_ultimate_internal_reasoning/` (376 lines) |
| `2f67976` | Fix exit-code masking in ultimate model harness (EXIT trap's `wait \|\| true` overwrote `exit 1`; now captures/restores `$?`) |
| `d1028be` | Raise Test 8 curl `--max-time` 5→25 (> MAX_GENERATION_TIME=20s) — pack 47/48→48/48, stable ×2 |

Production files touched: **none**. `handler_internal.go` verified byte-identical to 83814b0 (MD5).

## Documentation Updated
- [x] PACKS.md — 3 new mock-pack entries + statuses/last-run
- [x] MOCK_TESTS.md — new spec § Ultimate Internal Reasoning-Content Reproduction + Last Run
- [x] LESSONS/ — 3 files (Test 8 triage, third-converter reachability, e2e reproduction pattern)
- [x] COVERAGE.md — new test area + reachability gap
- [x] README.md — testing history row
- [x] RESULTS/2026-08-18-reasoning-content-ultimate-internal-verification.md (this file)

## Overall Status
| Task | Status |
|---|---|
| 1. Reproduce original scenario (symptom GONE?) | ✅ PASS — gone with fix, reproduces without it |
| 2. Regression (unit suites + mock e2e) | ✅ PASS (all packs green; 1 pre-existing harness defect found & repaired) |
| 3. Reachability probe (third converter) | ✅ Delivered — REACHABLE-UNDER-CONDITION, dormant |
| **Verdict** | **FIX VERIFIED — READY** |
