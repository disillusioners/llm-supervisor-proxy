# MERGE-GATE RE-RUN REPORT — real-streaming-default (round 2)

**Date**: 2026-08-28 · **Branch**: `feature/real-streaming-default` · **Re-run span**: `1d0c750 → 61fa02a` (64da4ae S3 fix + 61fa02a harness repair) plus tester-owned `6259b69` (matrix row) · **No merge, no push.**

## Verdict: 🟠 **NOT READY — one remaining item** (all 5 leader-specified re-run criteria PASS; one NEW feature-introduced wire finding, empirically proven vs base, needs leader adjudication or a small production fix)

## 1. S3 class — ✅ FIXED, 4/4
`test/e2e_anthropic_thinking_leak/` @ 61fa02a: **S1A ✅ S1B ✅ S2 ✅ S3 ✅** (exit 0, 0.142s).
S3 evidence: `persisted thinking == reasoning value (51 bytes, exact)`; persisted content contains the visible answer; wire shape unchanged for non-stream.

## 2. New regression tests under -race — ✅ 3/3, zero races
`TestLiveMode_NonStream_PersistedContentThinkingToolCalls`, `TestLiveMode_NonStream_BufferedParity` (pins byte-identical capture fields between live typed-mirror and buffered `extractOpenAIResponseContentFromJSON`), `TestLiveMode_NonStream_NoLiveArc_NoCapture` — all PASS under `-race -count=1`. Plus full raceA slice re-run (now 33 live tests incl. the 3 new): PASS, 24.1s, zero races.

## 3. Matrix gap closed (tester-owned) — ✅ committed `6259b69`
New `TestPathMatrix_R11_AnthropicClient_Internal_NonStream`: FE record thinking byte-exact (128B) AND content non-empty. Suite **21/21 PASS**. **Mutation-proven non-vacuous**: R11 run against `64da4ae~1` FAILS with the exact S3 symptom ("FE assistant.thinking is EMPTY"); PASS at HEAD.

## 4. Full suite at new HEAD — ✅ fully green (no carve-outs)
- Static: `go build ./...` ✅ · `go vet ./...` ✅ (61fa02a)
- Unit packs 13/13: proxy **449**+475 (incl. +3 new) · ultimatemodel 152+87 · translator 123+46 · gap 274 · testroot 35+8 · store 99 (+4 PG-skips; quarantine flake did NOT fire) · models 87+267 · toolrepair 17+105 · loopdetection 33 · auth 48+39 · token 23+123 · mcp 245+471 (3 env SSRF skips) · misc 284+119
- e2e Go 6/6: fe_reasoning **21/21** (with R11) · minimax 43/43 (drift 0, leaks 0) · reasoning_content 5+7 · ultimate_internal 1/1 · reasoning_content-dir 14 · **thinking-leak 4/4**
- Shell mocks 3/3: ultimate 49/49 (18s) · minimax 53/53 (12s) · **openai_internal_buffered 60/60 (14s — repaired harness verified, buffered mode via header)**

## 5. Fix wire-neutrality — ✅ proven at binary level
Live non-stream wire bytes are **byte-identical pre/post 64da4ae** (worktree comparison at 1d0c750 vs HEAD, same deterministic mock; sha256-stable). The fix's capture block sits strictly upstream of the unchanged `json.NewEncoder(w).Encode(resp)`.

## 🔴 NEW FINDING (surfaced by spot-check #5; NOT caused by the fix)
**Live-mode non-stream internal-Anthropic responses are OpenAI-shaped; base was Anthropic-shaped in both modes.**
- At HEAD: default/live → `{"object":"chat.completion","choices":[{"message":{...,"reasoning_content":...}}]}` (352B); with header → `{"type":"message","content":[{thinking},{text}]}` (336B). Structural split, diverges at char 3.
- **At base 9842c77 (empirical, worktree probe): BOTH modes return Anthropic-shape**, 345B, byte-identical except proxy-random `id`; the header does not exist at base (0 grep hits).
- Introduced by `e717be3` (phase-3 live branch routes non-stream internal to `handleNonStream`'s raw `json.NewEncoder(w).Encode(resp)`; buffered still translates via `TranslateNonStreamResponse`).
- Contradicts `docs/real-streaming-default.md` TL;DR: "Non-stream requests: Unaffected — header is a no-op" (true only for the external path).
- Impact: Anthropic-protocol clients calling `/v1/messages` with `stream:false` on internal models get an OpenAI-shaped JSON in the new default mode — protocol breakage vs base. Inconsistent with live STREAM mode on the same path (which emits proper Anthropic events per D8).
- First gate missed it: no test asserted non-stream Anthropic wire SHAPE (S3's wire checks are `t.Logf`-informational; M3's stream=false identity check ran on the OpenAI path).
- **Adjudication options**: (a) 🔧 RECOMMENDED production fix — route live non-stream internal through `TranslateNonStreamResponse` (mirrors buffered; S3's hard asserts unaffected since its wire checks are informational; M2-E then flips to a hard byte-identity assert with `id` normalized); (b) accept as documented behavior — requires docs TL;DR + D8 amendment and M2-E re-spec to per-mode shape stability.
- M2 harness: scenario E committed as ADVISORY (shape classifier + byte report, exit-neutral) pending adjudication; F (records) is a hard assert and PASSES post-fix.

## ensure.md
Critical: all green (build/vet/tests/frontend-build — frontend untouched by 64da4ae/61fa02a, no rebuild needed; prior gate's vite PASS stands). Improvement notices unchanged from round 1.

## Commits this round (tester-owned, on feature branch)
- `6259b69` — matrix gap closure R11 (test-only)
- (pending) — M2 E(adv)+F harness commit

## Bottom line
Round-1 blocker (S3) **fixed and quadruple-verified** (unit +3, e2e 4/4, race-clean, binary-level records, mutation-proven matrix row). Full suite green, harness repaired 60/60, fix wire-neutral. One new feature-introduced wire regression (non-stream shape split, e717be3) is empirically documented and needs a decision: small production fix (recommended) or documented acceptance. **NOT READY** solely on that item.
