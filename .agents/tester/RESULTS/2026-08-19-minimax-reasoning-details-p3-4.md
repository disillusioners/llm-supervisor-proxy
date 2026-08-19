# MiniMax reasoning_details Shell Harness (P3-4) — Run Report

**Date**: 2026-08-19  
**Branch**: feature/minimax-reasoning-details @ d5280ce  
**Script**: `test/test_mock_minimax_reasoning.sh`  
**Mock**: `test/mock_llm_minimax_reasoning.go`  
**Outer wrapper**: `timeout 300 bash test/test_mock_minimax_reasoning.sh`  
**Internal alarm**: 90s (reached at cleanup; all scenarios completed before alarm)  
**Actual runtime**: 90s (full clean exit)  
**Result**: **FAIL** (47/53 assertions PASS, 6 FAIL — all in T3b due to genuine PRODUCT bug)

---

## Per-T# result table

| T# | Result | One-line evidence |
|---|---|---|
| T1  | PASS | `/healthz` returns "ok"; admin API lists injected `mock-minimax-reasoning-model` |
| T2  | PASS | NS details response: `"reasoning_content":"think-A think-B"`, no `reasoning_details`/`audio_content` leak, content preserved |
| T3  | PASS | Captured upstream: top-level `"reasoning_split": true`, user message unchanged, no spurious `reasoning_details` |
| T3b | **FAIL** (PRODUCT BUG) | Assistant `reasoning_content` stripped + top-level `reasoning_split:true` set, but per-message `reasoning_details` array MISSING — see bug evidence below |
| T4  | PASS | NS both: details win; client sees `"reasoning_content":"think-A think-B"` exactly once; `from-reasoning_content` absent |
| T5  | PASS | Stream incremental: deltas `think-1`, `think-2`, `think-3` in order, 5 `data: {` lines, `[DONE]` last |
| T6  | PASS | Stream cumulative: deltas `A`, `B`, `C` only — no `AB`/`ABC` leakage, `[DONE]` last |
| T7  | PASS | Stream both: `from-details` appears exactly once (count=1); `from-reasoning_content` count=0 |
| T8  | PASS | Stream emptytext: `real-think` delta emitted; no empty `reasoning_content:""` delta |
| T9  | PASS | Stream multientry: `first` precedes `second` (line numbers P_F < P_S) |
| T10 | PASS | Flag absent + MODE-PLAIN: client sees `"reasoning_content":"legacy-think"`; captured upstream has NO `reasoning_split`/`reasoning_details` |
| T11 | PASS | Header hygiene: zero `x-proxy-interleaved-thinking` keys (any case) in ANY captured upstream headers |
| T12 | PASS | Non-MiniMax cred + flag ON: captured upstream has NO `reasoning_split`/`reasoning_details` (inertness preserved) |
| T13 | PASS | MODE-ERROR-500 + flag ON: client gets `{"error":{"type":"server_error","message":"openai: upstream exploded"}}`; no reasoning_details/text leak |
| T14 | PASS | Usage passthrough: `prompt_tokens:11`, `completion_tokens:7`, `total_tokens:18` preserved on NS translated response |
| T15 | PASS | Ultimate path: 2 captured requests for `ultimate-t15-trigger` (duplicate-hash retry fired); captured body has `reasoning_split: true` (ultimate request IS translated) |

**Total**: 47 PASS / 6 FAIL / 0 SKIP (out of 53 individual assertions across 15 scenarios).

---

## Capture excerpt — header hygiene (T11)

For each captured request, Python walked all header keys looking for any `interleaved-thinking` substring (case-insensitive). Result: **zero leaks** across all 13 captured upstream requests.

Sample captured request headers (typical, captured during T2):
```json
{
  "Accept-Encoding": "gzip",
  "Authorization": "Bearer mock-api-key",
  "Content-Length": "199",
  "Content-Type": "application/json",
  "User-Agent": "Go-http-client/1.1"
}
```

Note the **absence** of `X-Proxy-Interleaved-Thinking` (or any case variant) — the proxy correctly strips this header before forwarding on the external path.

---

## Capture excerpt — request translation (T3 / T3b)

### T3 (single user message — minimal translation):

```json
{
  "messages": [
    { "content": "ping MODE-NS-DETAILS", "role": "user" }
  ],
  "model": "mock-model",
  "reasoning_split": true,
  "stream": false
}
```

- ✓ Top-level `"reasoning_split": true` injected (gate fired).
- ✓ User message mode marker preserved (proxy did not mutate user content).
- ✓ No spurious `reasoning_details` (no input `reasoning_content` ⇒ nothing to translate).

### T3b (assistant message carries `reasoning_content`) — **BUG MANIFEST**:

```json
{
  "messages": [
    { "content": "hello", "role": "user" },
    { "content": "prev", "role": "assistant" },
    { "content": "ping MODE-NS-DETAILS-3msg", "role": "user" }
  ],
  "model": "mock-model",
  "reasoning_split": true,
  "stream": false
}
```

**Expected** (per spec): `messages[1]` should carry
```json
{ "content": "prev", "role": "assistant",
  "reasoning_details": [{ "type": "reasoning.text", "id": "reasoning-text-1",
                          "format": "MiniMax-response-v1", "index": 0,
                          "text": "earlier-think" }] }
```

**Actual**: assistant message is `{ "content": "prev", "role": "assistant" }` — `reasoning_content` was stripped (good — strip-and-replace half worked) but `reasoning_details` array is **absent** (bug).

---

## Detected product bug (T3b) — root cause hypothesis

The proxy on race-internal path (`pkg/proxy/race_executor.go:173`) calls
`translator.TranslateRequestBody(bodyMap)` which mutates bodyMap to put:

```go
msg["reasoning_details"] = []any{
    translator.ReasoningDetail{
        Type: "reasoning.text", ID: "reasoning-text-1",
        Format: "MiniMax-response-v1", Index: 0, Text: text,
    },
}
```

Then `convertToProviderRequest` calls `providers.HydrateReasoningDetails(msg)`
(`pkg/providers/openai.go:839`):

```go
raw, ok := msg["reasoning_details"].([]interface{})
// ok == true (any == interface{})
out := make([]ReasoningDetailEntry, 0, len(raw))
for _, rd := range raw {
    rdMap, ok := rd.(map[string]interface{})
    if !ok {
        continue  // ← BUG: rd is a struct, not a map; assertion fails silently
    }
    ...
}
return out  // returns empty []ReasoningDetailEntry{}
```

The translator writes **struct values** but `HydrateReasoningDetails` type-asserts
to `map[string]interface{}`. The assertion fails silently, the loop produces
an empty slice, the empty slice is dropped by `omitempty` during final marshal
in `OpenAIProvider.ChatCompletion` → the wire body loses the `reasoning_details`
array entirely.

This is **inert on race-external** because `executeExternalRequest` re-marshals
`bodyMap` directly (`json.Marshal(m)`) after translator mutation, so struct
values are encoded via their own json tags. The bug is specific to the typed
map→struct→marshal pipeline on race-internal (and likely ultimate-internal too).

**Repro**: send any request with `X-Proxy-Interleaved-Thinking: true` + model
with `internal:true` + `credential_id` resolving to `provider="minimax"` +
any assistant message carrying non-empty `reasoning_content`. The wire body
will lack the `reasoning_details` array even though `reasoning_split:true`
is set at top level.

**Fix path** (not applied per task constraint): either (a) translator writes
`[]map[string]any` instead of `[]any{Struct}` so the type assertion succeeds,
or (b) `HydrateReasoningDetails` uses reflection or json round-trip to handle
struct values, or (c) the translator stops being called on race-internal and
the typed struct hydration is the sole path.

---

## Compliance check

- Outer `timeout 300` wrapper: ✓
- Inner 90s alarm: ✓ (reached during cleanup but no scenario was interrupted)
- Ports 4005/4325 ONLY: ✓ (port 8088 untouched, verified by mock-test skill rules)
- Capture to `/tmp/minimax_reasoning_capture_4005.jsonl`: ✓ (13 lines)
- Mock killed on EXIT trap: ✓
- Proxy killed on EXIT trap: ✓
- No production code modified: ✓ (only test/ files created)

