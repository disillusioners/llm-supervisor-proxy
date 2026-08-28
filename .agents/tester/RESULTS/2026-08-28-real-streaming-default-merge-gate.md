# MERGE-GATE REPORT — real-streaming-default

**Date**: 2026-08-28
**Branch**: `feature/real-streaming-default` — feature HEAD `03a5339` (12 commits after base `9842c77`) + authorized test re-base commit `22e76d6`
**Workdir**: /Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy
**Verdict**: 🔴 **NOT READY** — 1 REAL feature-introduced bug found (internal-Anthropic non-stream persistence). Everything else GREEN (22/23 nodes PASS; 1 pack red solely due to that bug; 1 unrelated pre-existing script-rot FAIL documented).

## Scope Decision
Full suite warranted and executed (merge gate, cross-module streaming change). `go test ./...` parity achieved via 13 registered unit packs + 3 new coverage-gap packs + inline slices — every package with tests covered, including 7 previously-uncovered ones (credentiallb, proxyheader, proxy/normalizers, proxy/token re-verified, proxy/translator, loopdetection/fingerprint, store parent, test/ root). All 6 in-repo e2e Go suites + 3 real-binary shell mocks + 3 new real-binary smoke mocks executed. Frontend untouched by the change (verified via git diff) — vite build still verified green.

## 1. Static checks — ALL PASS
- `go build ./...` ✅ exit 0
- `go vet ./...` ✅ exit 0
- Frontend `npm run build` ✅ (vite 5, 1.17s; standing ~30 `tsc --noEmit` errors are pre-existing debt, not in blast radius — no frontend file changed)

## 2. Unit packs — 13/13 PASS (see PACKS.md for per-pack rows)
proxy (446+475, 7 branch-gated skips), ultimatemodel (152+87, +7 vs baseline), **translator NEW** (169 incl. 19 TestIncrementalStreamTranslator_*), **gap NEW** (274: credentiallb/proxyheader/normalizers/fingerprint/store-parent), **testroot NEW** (35), store (99+4 PG-skips; quarantined CloseLifecycle did NOT fire), models (87+267; StreamDeadline change green), toolrepair (17+105), loopdetection (33), auth (48+39), token (23+123), mcp (245+471, 3 env SSRF skips), misc (284+119).

## 3. Race detector — 3/3 PASS, zero races
- raceA proxy regex (`TestRealStreaming|TestLiveRelay|TestLiveMode|TestUltimateCapturePersistence`): 24s clean
- raceB ultimatemodel (`TestUltimate*`): 2.8s clean
- raceC translator full: 1.1s clean

## 4. e2e Go suites — 5/6 green, 1 red = the real bug
- fe_reasoning_observability: **PASS 20/20** (closure 4/4 + matrix 16/16 — capture taps mode-independent)
- e2e_minimax_reasoning: **PASS 43/43** (drift delta 0, header hygiene 0 leaks)
- e2e_reasoning_content: **PASS** (5+7)
- e2e_ultimate_internal_reasoning: **PASS 1/1**
- test/reasoning_content: **PASS** (14 subtests)
- **anthropic_thinking_leak: S1A ✅ S1B ✅ S2 ✅ / S3 ❌ REAL BUG** (below)

## 5. Real-binary shell mocks — 2/3
- ultimate_model_shell_mock: **PASS 49/49** (~150s; trigger schedule + live streaming on ultimate paths)
- minimax_reasoning_shell_mock: **PASS 53/53** (11s — prior near-cap warning resolved, cold-build artifact)
- openai_internal_buffered_shell_mock: **FAIL 0/60 — PRE-EXISTING SCRIPT ROT, not this feature.** Legacy `credential_id` model-creation payload rejected by credential-LB schema (pkg/ui/server.go:436 requires `credentials: []`). Broken at base (credential-LB merge predates branch cut); last PASS db7aca0+. Needs ~3-line payload fix + should start sending the buffer header to keep testing buffered mode post-feature.

## 6. Authorized test re-base — DONE, commit `22e76d6`
`test/e2e_anthropic_thinking_leak/`: S1 split into S1A (buffered mode via `X-LLMProxy-Buffer-Response: true`, original absence assertions preserved = buffered contract) + S1B (live mode, positive D8 assertions: thinking_delta + type:thinking present, reasoning inside thinking_delta payloads, reasoning_content absent, sink capture preserved 51-byte exact, content intact). S2/S3 untouched. 2 files, +169/−33.

## 7. Original-scenario live-binary smokes — M1/M2/M3 ALL PASS
**M1 OpenAI path** (test/mock_rsd_m1_openai_ttfb.sh, ports 10110/10111, 3× stable):
- DEFAULT: TTFB(first `data:`) 103ms vs total 1536ms; 5 inter-chunk gaps ~300ms (incremental, not burst) ✅
- BUFFERED (`true`): TTFB 1603ms, spread 0ms single burst, content identical ✅
- C (info): raw bytes differ — live emits `: keepalive` SSE comments during the stream; assembled content byte-equal. NOTE: TTFB must be measured at first `data:` line — proxy sends `: connected` preamble immediately in BOTH modes (handler.go:897).
- /healthz 200 after both ✅
**M2 /v1/messages + ultimate + UI** (ports 10120/10121):
- DEFAULT: TTFB 2ms, spread 1520ms, thinking_delta on wire (D8) ✅
- BUFFERED: TTFB 1524ms, spread 0ms, pre-feature shape (no thinking blocks) ✅
- Ultimate external: documented-impractical (OpenAI-wire passthrough vs Anthropic client — handler_external.go:115); partial evidence captured; ultimate paths instead covered green by shell mock 49/49
- UI records: /ui/ 200; BOTH modes → records with content+thinking+status=completed ✅ (usage=null on Anthropic path = PRE-EXISTING gap, proven by buffered-mode parity; OpenAI/ultimate paths do populate usage)
**M3 edge cases** (ports 10130/10131, 21/21):
- Truth table 12/12 vs docs (absent→live; bare/true/TRUE/1/yes/on→buffered; false/0/no/off/garbage→live)
- Multi-line first-wins both directions ✅ (comma-joined single-line = one unknown value → live, per docs caveat)
- stream=false ± header: byte-identical 285B JSON, no SSE ✅
- Client disconnect mid-stream (live): /healthz 200, no panic/SIGSEGV ✅ (9842c77 class stays fixed)

## 8. 🔴 REAL BUG (feature-introduced) — S3 class
**Internal-Anthropic NON-STREAM persistence empty in live mode (the new default).**
- Symptom: persisted assistant records have `Thinking=""` AND `Content=""` while the client wire response is correct.
- Bisect: PASS @ base 9842c77 and @ 9148de3; **first-bad `e717be3`** (phase 3 incremental translator) through HEAD.
- Root cause: live branch of `doAnthropicInternalRequest` (handler_anthropic.go) drives the TRUE `w` via `HandleRequest`; `handleNonStream` (internal_handler.go:369-379) writes wire JSON but never calls `liveTranslator.ProcessEvent` nor the thinking sink — the persistence mirror then copies empty translator `StreamState` into the arc builders.
- Impact: UI/request records lose content+thinking for every non-stream `/v1/messages` internal request (default mode). Violates "stream=false unaffected" for record capture.
- Fix direction (not applied — production frozen): wire `handleNonStream` through `ProcessEvent`+sink, or mirror directly from the `ChatCompletionResponse` in the live branch.
- Detection credit: `TestS3_InternalNonStream_PersistedThinking` (only coverage catching it — fe_reasoning matrix has no anthropic-internal-nonstream row).
- Full analysis: LESSONS/2026-08-28-rsd-s3-nonstream-persistence-bug.md

## 9. ensure.md status
- Critical: Go unit tests ✅ (13/13 packs) · vet ✅ · build ✅ · frontend build ✅ (vite; see notice below)
- Critical caveat: full-suite green is impossible until S3 bug fixed (it is a production bug, correctly red).
- Important/Nice-to-have (peak-hour, migration 018, races): out of this change set's blast radius; races explicitly probed clean (3 slices).
- **Improvement notices**: (1) "go test ./..." should reference packs (validated via packs this gate); (2) "Frontend builds without TypeScript errors" is unmeetable as written (standing 30 tsc errors, pre-existing) — suggest "vite build succeeds" wording.

## 10. Quarantine
No new quarantines. TestStoreEngine_CloseLifecycle (known macOS flake) did NOT fire this gate. S3 is a deterministic real bug (not flaky) — stays red until fixed. openai_internal_buffered script rot is deterministic debt, tracked in PACKS.md (needs-fix), not quarantine-worthy.

## Overall
- Unit 13/13 ✅ · Race 3/3 ✅ · e2e 5/6 (1=real bug) · shell mocks 2/3 (1=pre-existing rot) · smokes 3/3 ✅ · re-base committed `22e76d6`
- **Testing Complete: ❌ NOT READY — fix S3-class persistence bug (e717be3 regression), then re-run anthropic_thinking_leak pack (expect 4/4) + M2-D non-stream variant.**
