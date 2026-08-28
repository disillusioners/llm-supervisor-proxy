# S3-class bug: internal-Anthropic non-stream persistence empty in live mode (real-streaming-default)

**Found**: 2026-08-28 merge gate, branch feature/real-streaming-default
**Detecting test**: `TestS3_InternalNonStream_PersistedThinking` (test/e2e_anthropic_thinking_leak/e2e_anthropic_thinking_leak_test.go:192-239)
**Severity**: 🔴 critical for merge — silent record data loss, user-visible in UI

## Symptom
`S3: persisted thinking="" want "Hmm, internal deliberation over the leak constraint"`
`S3: persisted content="" missing "Visible answer for the anthropic client."`
Client wire response is CORRECT (visible answer present, expected non-stream shape); only the persisted assistant record is empty.

## Bisect (worktree method, single-test runs ~0.03s each)
| Commit | S3 |
|---|---|
| 9842c77 (base) | PASS |
| 9148de3 (phase 1 header parsing) | PASS |
| **e717be3 (phase 3 incremental translator)** | **FAIL — first bad** |
| 1e8d629, c101b26, 03a5339 (HEAD) | FAIL |

## Root cause
`e717be3` branched `doAnthropicInternalRequest` on `arc.bufferMode`. In live mode (default — no `X-LLMProxy-Buffer-Response`), the live branch calls `internalHandler.HandleRequest(…, w, arc.isStream, …)` with the TRUE writer. For non-stream requests that routes to `handleNonStream` (pkg/proxy/internal_handler.go:369-379), which only does `provider.ChatCompletion` + `json.NewEncoder(w).Encode(resp)` — it never calls `liveTranslator.ProcessEvent(...)` and never writes `capturedThinking`. After return, the persistence mirror in the live branch reads `arcTranslator.State()` (empty) and `capturedThinking` (empty) into `arc.accumulatedResponse`/`arc.accumulatedThinking` → finalize persists empty strings. Stream mode is unaffected (handleStream has the ProcessEvent arms).

## Why only this test caught it
- fe_reasoning_observability matrix: anthropic-client rows are internal-STREAM (R9) + external-NON-stream (R10) — no internal-non-stream row.
- Unit tests: proxy pack green — the live non-stream persistence path is only asserted end-to-end here.

## Fix direction (production; NOT applied during gate)
(a) In `handleNonStream`, before writing the wire: `liveTranslator.ProcessEvent(content, reasoningContent, "", toolCalls)` + `capturedThinking.WriteString(reasoning)`, or
(b) In the live branch of `doAnthropicInternalRequest`, mirror content/thinking directly from the `ChatCompletionResponse` instead of relying on translator StreamState.
Estimated ~10-20 lines.

## Post-fix verification protocol
1. `timeout 300 go test ./test/e2e_anthropic_thinking_leak/ -v -count=1 -timeout 240s` → expect S1A/S1B/S2/S3 all PASS
2. Add/extend an fe_reasoning_observability matrix row for anthropic-internal NON-stream (coverage gap this bug exploited)
3. Optional: M2-style binary smoke with a non-stream /v1/messages request asserting record content+thinking

## Related (pre-existing, NOT this feature)
- Anthropic internal-translation path never populates `reqLog.Usage` (handler_anthropic.go:1415 finalizeAnthropicSuccess; translator emits usage on wire only). Proven pre-existing: buffered mode (byte-for-byte pre-feature behavior) shows `usage=null` too. OpenAI race (handler.go:1264) and ultimate (handler.go:768) paths DO populate.
