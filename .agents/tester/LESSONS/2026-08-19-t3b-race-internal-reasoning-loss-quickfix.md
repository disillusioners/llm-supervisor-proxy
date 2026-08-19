# T3b: Race-internal silent reasoning destruction — found, verified, fixed (P3-4 gate)

**Date**: 2026-08-19 · **Branch**: `feature/minimax-reasoning-details` · **Commits**: harness `882fa3f`, fix `068317c`, harness repair `1344380`

## Symptom
P3-4 shell harness (`test/test_mock_minimax_reasoning.sh`) T3b: with `X-Proxy-Interleaved-Thinking: true` + MiniMax cred + `internal:true` model, a request whose assistant message carried `reasoning_content` reached the upstream with `reasoning_content` STRIPPED and NO `reasoning_details` array — replayed reasoning silently destroyed. All other 47 assertions passed (race-external translated correctly).

## Root cause (code-verified, minimal repro in /tmp/t3b_repro)
1. `translator.TranslateRequestBody` (pkg/proxy/translator/minimax.go:133-141) writes `msg["reasoning_details"] = []any{ReasoningDetail{...}}` — STRUCT values inside `map[string]any`.
2. Race-internal path (`race_executor.go:843`) then calls `providers.HydrateReasoningDetails(msg)`, which type-asserts each element to `map[string]interface{}` (openai.go:846). Struct ⇒ assertion fails silently ⇒ `continue` ⇒ empty `[]ReasoningDetailEntry`.
3. `ChatMessage.ReasoningDetails` has `json:"reasoning_details,omitempty"` ⇒ final `json.Marshal` (openai.go:80) drops the empty slice ⇒ field absent on wire.
- Race-external unaffected: re-marshals `bodyMap` directly (struct values encode via own json tags).
- Ultimate-internal unaffected: constructs the typed slice directly (handler_internal.go:88-122), never reaches hydration.

## Fix evolution (the interesting part)
- **Attempt 1 — translator emits `map[string]any` entries: STOPPED (byte-fatal).** Empirical byte-check proved `encoding/json` serializes struct fields in declaration order (`type,id,format,text`) but map keys alphabetically (`format,id,text,type`); no map shape can match. This would have broken the P3-2 byte-identical contract on race-external. Lesson: **when a typed struct and a map produce the "same" JSON, they differ in key order — byte-identity assertions constrain fix loci.**
- **Attempt 2 (shipped) — hydrate-boundary type-switch** in `HydrateReasoningDetails` (pkg/providers/openai.go): accepts both `map[string]interface{}` (unchanged) and `translator.ReasoningDetail` (new branch, inline per-element map conversion). No wire-shape change, no import cycle (openai.go already imports translator for ExtractEntryText), no new exported symbol. Regression test: `TestHydrateReasoningDetails_AcceptsTranslatorStruct` (openai_reasoning_test.go).
- **Harness assertion repair**: T3b asserted `'"index": 0'` on the wire — structurally impossible: both `ReasoningDetail` and `ReasoningDetailEntry` carry `Index int json:"index,omitempty"` and the translator always emits `Index:0`, so the key is ALWAYS omitted. Replaced with `assert_not_contains '"index"'`. Lesson: **assert wire contracts that survive omitempty, not field-presence fantasies.**

## Verification chain
translator/proxy/ultimatemodel/providers packs PASS · vet clean · /tmp repro PATH A (race-internal) now byte-equals PATH B (ult-internal control) · harness 53/53 (T3b 6/6).

## Residual
- `translator.ReasoningDetail` struct remains (kept to minimize diff; hydrator now consumes it — no longer dead).
- Pre-existing flaky panic `stream_buffer.go:166`/`race_executor.go:1434` seen 1/~7 pkg/proxy runs (timing-sensitive, 2s streaming deadline; unrelated to this fix).
