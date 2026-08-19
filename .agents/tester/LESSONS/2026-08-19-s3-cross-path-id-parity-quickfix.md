# S3: Cross-path reasoning-text id divergence — found by e2e, invisible to unit tests

**Date**: 2026-08-19 · **Branch**: `feature/minimax-reasoning-details` · **Commits**: e2e suite `166aa7f`, fix `b2dfde0`

## Symptom
P3-5 e2e suite S3/ult-int: `[user, assistant(reasoning_content), user]` → ultimate-internal emitted
`reasoning_details[0].id = "reasoning-text-2"` while race-internal and both external paths emitted
`"reasoning-text-1"` for identical input.

## Root cause
`pkg/ultimatemodel/handler_internal.go:106` (W5 typed loop) used `fmt.Sprintf("%s%d", prefix, i+1)`
— the MESSAGE index — instead of a monotonic counter over reasoning-carrying messages (the
translator's documented contract). The twins were built separately (typed setter vs map-mode
translator) and drifted.

## Why unit tests missed it
Grep proved ZERO unit tests asserted the id scheme anywhere. The divergence only becomes visible
when two paths are compared on identical input — a cross-path e2e assertion. Lesson:
**contract details shared by parallel implementations need a cross-implementation parity test,
not per-implementation unit tests.** The P3-5 S3 assert (`id == "reasoning-text-1"` on ult-int)
is now the permanent regression gate.

## Fix
Counter `n` incremented only when a message qualifies (after the `ReasoningContent == ""` skip),
exactly mirroring `TranslateMessagesReasoning` (`if !ok || text == "" { continue }` before
`counter++`, minimax.go:126-129). Empty reasoning consumes no slot on either implementation.
+14/−1 in one production file. Chain: ultimatemodel 4.4s ✅ · e2e 43/43 ✅ · proxy 24.1s ✅ ·
translator ✅ · vet ✅ · shell harness 53/53 ✅ (T15 unaffected — single-reasoning-at-index-0,
where both schemes agree).
