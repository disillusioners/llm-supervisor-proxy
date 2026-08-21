# E2E Anthropic Thinking-Leak Spot-Check — Results

**Date**: 2026-08-21 · **Branch**: `fix/ui-reasoning-observability` · **Test package**: `test/e2e_anthropic_thinking_leak/`

## Summary

**RESULT: PASS — 3/3 scenarios** (runtime ~0.11s; dual-layer timeout: outer `timeout 300` + inner `-timeout 240s`)

The dev-verify leak (anthropic-format client streaming + thinking events leaking thinking blocks to the client wire) is **closed** on the internal path: the thinking-sink invariant (`internal_handler.go:225-242`, installed at `handler_anthropic.go:482`) keeps ALL thinking bytes off the wire while persisting the concatenated reasoning via the side-channel sink. The external path still translates reasoning_content → thinking_delta by design (no over-swallow).

## Command

```bash
timeout 300 go test ./test/e2e_anthropic_thinking_leak/ -v -count=1 -timeout 240s
```

## Per-Scenario Results

### S1 — INTERNAL path, stream: the leak spot-check → **PASS**
- Routing proof: captured upstream body `model="gpt-internal-leak-test"` (internal rewrite confirmed — internal path exercised, not external fallback)
- **(a) No-leak** — searched the full recorder (wire) body for 5 substrings, all ABSENT:
  - `"Hmm, internal"` → not found
  - `" deliberation over the leak constraint"` → not found
  - `"reasoning_content"` → not found
  - `"thinking_delta"` → not found
  - `"type":"thinking"` → not found
- **(b) Captured thinking (sink's other side)**: persisted `assistant.Thinking` = `"Hmm, internal deliberation over the leak constraint"` — non-empty and byte-exact vs the 2 upstream reasoning chunks concatenated
- **(c) Content intact**: wire contains `"Visible answer for the anthropic client."`; persisted content matches
- Wire length 808 bytes; request status `completed`

### S2 — EXTERNAL path, stream: over-swallow regression guard → **PASS**
- Routing proof: captured upstream body `model="unregistered-anthropic-external"` (raw name — external path confirmed)
- Wire **DOES** contain `"thinking_delta"` (found) — by-design external translation alive
- Wire contains both reasoning fragments (found) — translator emitted the accumulated thinking delta
- Wire contains content (found); persisted thinking == concatenated reasoning
- Guards that the sink did NOT swallow the external path

### S3 — INTERNAL path, non-stream → **PASS**
- Routing proof: `model="gpt-internal-leak-test"` (internal path confirmed)
- **Persisted thinking == reasoning value** (51 bytes, exact)
- Content present on wire + persisted
- **Wire classification**: wire contains a translated thinking block (371-byte wire). Base-commit differential at the fix's parent `effc345` produced the **identical 371-byte wire** — the block originates from `translator/response.go` (`extractContentFromOpenAIMessage`), which is **unchanged since base `fea5874`**. It is pre-existing by-design non-stream translation, NOT a fix-introduced leak; pre-fix vs post-fix bytes are identical (byte-identity constraint holds). The fix's non-stream guarantee (persisted thinking) is asserted as a hard contract.

## Non-Vacuity Proof (mutation test)

To prove S1 actually detects the leak, the leak was re-injected in an isolated git worktree (leak shape: `case "thinking"` writing a `reasoning_content` SSE chunk to the recorder in addition to the sink — the exact transient pre-fix state the dev-verify pass caught):

- With mutation: `--- FAIL: TestS1_InternalStream_ThinkingLeakSpotCheck` — 4 LEAK detections (`"Hmm, internal"`, `" deliberation over the leak constraint"`, `"thinking_delta"`, `"type":"thinking"`), and the mutated wire shows the leaked Anthropic thinking block: `content_block_start {"content_block":{"thinking":"","type":"thinking"}}` + `content_block_delta {"delta":{"thinking":"Hmm, internal deliberation over the leak constraint","type":"thinking_delta"}}`
- Without mutation (current branch): PASS
- Worktree discarded afterward; production files untouched in the real tree

Sensitivity at the fix's parent `effc345`: S1/S2 PASS there too — the sink landed in `d3efad5` (persist thinking stream events on internal path) and `2e183d3` hardened it (double-set guard, ResetThinkingSink, recorder-code check ordering, W4 fallback reset). The mutation test demonstrates S1 catches the pre-`d3efad5` leak shape on any regression.

## Harness Notes

- In-process harness (e2e_minimax-style): capturing httptest upstream (ephemeral ports only; 8088 never touched), temp SQLite + real migrations, real `auth.TokenStore`, `proxy.NewHandler`; driver calls `h.HandleAnthropicMessages(rr, req)` directly
- Quick fixes applied during development: none to production code; harness had one compile-fix iteration (mock capture wiring) before the first green run
- Cleanup verified: no port leaks (httptest ephemeral + `t.Cleanup`), temp dirs auto-removed

## Conclusion

| Scenario | Result | Key evidence |
|---|---|---|
| S1 internal stream leak | **PASS** | 5/5 leak substrings absent; persisted thinking byte-exact; content intact |
| S2 external stream translate | **PASS** | thinking_delta + reasoning fragments present on wire |
| S3 internal non-stream | **PASS** | persisted thinking exact; wire block pre-existing (base-differential identical) |

Leak constraint: **closed**. Regression guard: **armed** (mutation-proven detection).
