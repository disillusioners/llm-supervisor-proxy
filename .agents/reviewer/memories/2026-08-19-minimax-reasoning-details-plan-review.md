# 2026-08-19 — minimax-reasoning-details plan review (Deep-Review council)

**Verdict: CRITICAL GAPS** (unanimous, 2 councilors: agentic + coding). Branch `feature/minimax-reasoning-details @ ac877ea`, implementation not started.

## Root cause pattern
The plan designs hooks for the **byte-level** (external) proxy paths and assumes the **typed** (internal) paths share the same shape. They do not: internal paths go map → typed `*ChatCompletionRequest` → `json.Marshal`, and internal stream loops iterate typed `StreamEvent`s (parsed inside `openai.go`), not raw SSE lines.

## 🔴 Blockers (B1–B6)
- **B1** `reasoning_split: true` never reaches the wire on internal paths — struct has no field, `Extra` is `json:"-"` (dead). `pkg/providers/interface.go:39`, `openai.go:67/143`.
- **B2** Internal stream paths never see raw SSE; `reasoning_details` dropped at provider parse time (`extractReasoningContent` reads only the string field). Fix: per-entry `thinking` event extraction in `openai.go` stream parser. `openai.go:375-401/500-513`, `race_executor.go:509`, `handler_internal.go:264`.
- **B3** Flag unavailable at race-internal wiring site — `executeInternalRequest(ctx, cfg, rawBody, req)` has no `rc`/`*http.Request`; "rc rides by reference" premise false (`handler.go:765`, `race_executor.go:53`). Fix: thread explicit `interleaved bool`.
- **B4** `x-proxy-interleaved-thinking` LEAKS on race-external — `race_executor.go:163-166` strips only `x-llmproxy-*`; P1-7 patches only `handler_external.go:79-87`. Independently verified by both councilors. Also self-contradicts P3-2 header assertions.
- **B5** `upstreamProvider` derivation for ultimate-external unspecified — `CredentialID` read by no external path today; gate may be permanently false on ultim-ext.
- **B6** Race-external request-side translation unwired — 5th mutation site `race_executor.go:147-155` omitted from P1-8's list of 4.

## 🟡 Notable warnings
W1 provider gate case-sensitivity (precedent `strings.ToLower` at `handler_anthropic.go:297`); W2 both-fields coexistence vs `case "thinking"`; W3 translator-vs-normalizer ordering (`handler_external.go:234`); W4 P1-8 bytes wrapper contradicts arch §8 map-core guidance; W7 unconditional `x-proxy-*` strip is an ungated flag-absent behavior change; W11 `omitempty` doesn't drop empty `[]` arrays.

## Lesson
For plan reviews in this repo: always verify hook points against **both** path families (external byte-level vs internal typed). The 4-path matrix (race-int/ext, ultimate-int/ext) has two distinct shapes; plans written against one family silently no-op on the other.

---

# Round 2 (2026-08-19) — re-review after blocker fixes

**Verdict: APPROVED** (unanimous, 2 councilors; 0 critical). All B1–B6 verified RESOLVED in operative plan text (not just Amendment Logs), with source verification.

## Residual (non-blocking) items
- **D1 drift** 🟡 — 5 stale general-`x-proxy-*`-strip references contradict binding D4: `phase1-plan.md:54,:72`, `phase2-plan.md:35`, `architecture-recommendation.md:256,:324` (§9 Q3 lists strip-scope as still-open, contradicting §11 in the same doc).
- **D2 invariant wording** 🟡 — "byte-identical all four paths" imprecise: D2's openai.go extraction is unconditional/data-driven. Real invariant = flag gates REQUEST-side only; response-side extraction is data-driven, naturally inert for non-MiniMax (R4). Add rationale + "do NOT add providerIsMiniMax inside the shared parser" guard note (prevents future wrong-layer gating).
- **D3** 🟡 — `technical-analysis.md:37,213,238` round-1 struct framing not fully purged.
- **D4 (NEW, leader decision)** 🟡 — openai.go importing `translator.extractEntryText` = `providers → translator` dependency inversion. Option (a) move `ReasoningDetail` type to pkg/providers; option (b) document + helper-only import. Council recommends (b).
- **D5** 🟡 — P2-1a implementation locus (which function, between `json.Unmarshal` :374 and `extractReasoningContent` :396) + parse ordering (check reasoning_details first) implicit.
- Twin-C de-scope: sound, cleanly bounded, zero stale coverage references.
- `TranslateNonStreamResponseTyped` sweep: complete, grep-verified (all 14 remaining mentions are docs-of-removal).

## Lesson (R2)
Amendment passes leave stale copies in Risks/Exit-Criteria/Q&A sections — future re-reviews must grep for rejected-scope language across ALL sections, not just task lists. Also: verification round ≠ re-review — scope councils to claim-verification + new-drift detection, explicitly excluding re-litigation.
