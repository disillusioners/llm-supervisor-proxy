# Plan: Support OpenCode Retry After Stream End

## Problem Statement

OpenCode uses `SessionRetry.retryable()` to determine if an error should trigger a retry. The proxy's current error JSON formats do not match OpenCode's expected patterns, causing retries to not trigger after stream errors.

### Current Proxy Error Format

```json
{"error": {"message": "...", "type": "..."}}
```

### OpenCode's Expected Patterns

| Pattern | Triggers Retry | Return Value |
|---------|---------------|--------------|
| `json.type === "error"` + `json.error.type === "too_many_requests"` | ✅ | `"Too Many Requests"` |
| `json.type === "error"` + `json.error.code` contains `"rate_limit"` | ✅ | `"Rate Limited"` |
| `json.code` contains `"exhausted"` | ✅ | `"Provider is overloaded"` |
| `json.code` contains `"unavailable"` | ✅ | `"Provider is overloaded"` |
| Any valid `{"type":"error",...}` JSON | ✅ | JSON string (fallback) |
| `{"error":{"message":"..."}}` (current proxy) | ❌ | `undefined` |

### The Gap

The proxy sends errors with `type` and `message` nested under an `error` key, but OpenCode expects:
1. `type: "error"` at the **root level**
2. Error details under `error` key (type/code/message)
3. Optional `code` field at root for exhaustion detection

---

## Design Decision: Backward Compatibility

**Decision: Full Replacement** — OpenCode-compatible format replaces existing error formats entirely. No legacy support needed.

### SSE Event Format: Per Endpoint

| Endpoint | Format | Reason |
|----------|--------|--------|
| OpenAI-compatible | `data: {"type":"error","error":{...}}\n\n` | OpenAI SDK expects `data:` prefix |
| Anthropic-compatible | `event: error\ndata: {"type":"error","error":{...}}\n\n` | Anthropic uses adapter pattern, `event: error` |

### Response Format: Streaming vs Non-Streaming

| Response Type | Format |
|---------------|--------|
| Non-streaming (HTTP body) | `{"type":"error","error":{"type":"...","code":"...","message":"..."}}` |
| Streaming (SSE chunks) | `data: {"type":"error","error":{...}}\n\n` |

### Rate Limit Detection: Proxy Adds Code After All Models Fail

**Rule:** Only when race retry has exhausted **all models** (main + fallback), proxy adds `code: "rate_limit"` to the final error.

**Flow:**
```
1. Main model fails with 429
2. Spawn fallback model
3. Fallback model also fails with 429
4. All models exhausted → Final error gets:
   {"type":"error","error":{"type":"rate_limit","code":"rate_limit","message":"All models rate limited"}}
```

**Rationale:** If we have working models, no need to signal retry with rate_limit code. Only signal when everything is exhausted.

**Important:** This only applies to race retry. If race retry is disabled or only one model is configured, rate limit code is still added (since "all models" = the only model).

### Error Code Mapping

| Error Type | `type` | `code` | When Applied |
|------------|--------|--------|--------------|
| Rate limit (429) | `rate_limit` | `rate_limit` | Only after all models exhausted in race retry |
| Provider exhausted | `error` | `exhausted` | On model capacity errors |
| Service unavailable | `error` | `unavailable` | On upstream errors (502, 503) |
| Context overflow | N/A | N/A | Should NOT trigger retry (OpenCode handles separately) |
| Authentication | `authentication_error` | - | Non-retryable |
| Generic upstream error | `upstream_error` | - | Default for unclassified errors |

---

## Implementation Plan

### Phase 1: Define Error Format Constants

**File:** `pkg/models/errors.go` (new)

```go
// OpenCode-compatible error response format (non-streaming HTTP body)
type OpenCodeErrorResponse struct {
    Type  string        `json:"type"`
    Error ErrorDetails  `json:"error"`
}

type ErrorDetails struct {
    Type    string `json:"type,omitempty"`
    Code    string `json:"code,omitempty"`
    Message string `json:"message"`
}
```

### Phase 2: Update HTTP Error Responses (Non-Streaming)

**File:** `pkg/proxy/handler.go`

| Function | New Format |
|----------|------------|
| `sendError()` | `{"type":"error","error":{"type":"...","code":"...","message":"..."}}` |
| `sendAuthError()` | `{"type":"error","error":{"type":"authentication_error","message":"..."}}` |
| HTTP 429 (after all models fail) | `{"type":"error","error":{"type":"rate_limit","code":"rate_limit","message":"Rate limit exceeded"}}` |
| HTTP 503 | `{"type":"error","error":{"type":"too_many_requests","code":"unavailable","message":"Service unavailable"}}` |

### Phase 3: Update SSE Error Chunks (OpenAI Endpoint)

**File:** `pkg/proxy/handler.go` (line 684)

```go
// New format:
fmt.Fprintf(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"message\":\"Streaming error: %v\"}}\n\n", err)
```

**File:** `pkg/proxy/adapter_openai.go` (lines 134-144)

```go
func (a *OpenAIAdapter) WriteStreamError(w io.Writer, errorType, message string) error {
    errResp := fmt.Sprintf(`{"type":"error","error":{"type":"%s","message":"%s"}}`,
        errorType, message)
    _, err := fmt.Fprintf(w, "data: %s\n\n", errResp)
    return err
}
```

### Phase 4: Update SSE Error Chunks (Anthropic Endpoint)

**File:** `pkg/proxy/adapter_anthropic.go`

Use adapter pattern — same format but with `event: error` prefix:

```go
func (a *AnthropicAdapter) WriteStreamError(w io.Writer, errorType, message string) error {
    errResp := fmt.Sprintf(`{"type":"error","error":{"type":"%s","message":"%s"}}`,
        errorType, message)
    _, err := fmt.Fprintf(w, "event: error\ndata: %s\n\n", errResp)
    return err
}
```

### Phase 5: Race Retry Error Propagation

**File:** `pkg/proxy/race_coordinator.go`

When all models exhausted:
1. Collect last error from each failed model
2. Use primary model's error type for final response
3. Add `code: "rate_limit"` if **any non-context-overflow model** returned 429

```go
// Pseudocode:
finalError := buildOpenCodeError(lastModelError)

// Only add rate_limit code if:
// - Any model returned 429
// - AND error is NOT context overflow
if anyModel429 && !isContextOverflow(finalError) {
    finalError.Error.Code = "rate_limit"
}
```

**Important:** Skip adding `code: "rate_limit"` if the error is context overflow — preserve `type: "context_length_exceeded"` for OpenCode's compaction detection.

### Phase 6: Update Ultimate Model Errors

**File:** `pkg/ultimatemodel/handler.go`

```go
// New format:
{"type":"error","error":{"message":"...","type":"ultimate_model_retry_exhausted","code":"exhausted","hash":"..."}}
```

### Phase 7: Error Code Mapping for Upstream 429s

**Rule:** Proxy adds `code: "rate_limit"` only after race retry exhausts all models.

**Implementation locations:**
- `pkg/proxy/race_coordinator.go` — After collecting all model results
- `pkg/proxy/handler.go` — When forming final error response

---

## Files to Modify

| File | Changes |
|------|---------|
| `pkg/models/errors.go` | Add OpenCode-compatible error structs |
| `pkg/proxy/handler.go` | Update `sendError()`, `sendAuthError()`, stream error chunks (OpenAI format) |
| `pkg/proxy/adapter_openai.go` | Update `WriteStreamError()` to new format |
| `pkg/proxy/adapter_anthropic.go` | Update `WriteStreamError()` to use `event: error` prefix |
| `pkg/proxy/race_coordinator.go` | Add `code: "rate_limit"` after all models exhausted |
| `pkg/ultimatemodel/handler.go` | Update retry exhaustion error format |

## Testing Requirements

### Unit Tests

1. **Error format tests** (`pkg/models/errors_test.go`) — Verify generated error JSON matches OpenCode expectations
2. **SSE chunk tests** — Verify streaming error chunks are parseable and have correct format
3. **Race retry error tests** — Verify `code: "rate_limit"` added only after all models fail

### Integration Tests

1. **OpenCode retry simulation test** — Send error response, verify OpenCode's `retryable()` would trigger
2. **End-to-end streaming test** — Simulate stream with error, verify retry behavior
3. **Race retry exhausted test** — Verify rate limit code added after all models fail

---

## Open Questions for Team Review

~~1. **Breaking change acceptable?**~~ ✅ **Resolved: Full replacement**
~~2. **SSE event format**~~ ✅ **Resolved: Per endpoint (OpenAI `data:`, Anthropic `event: error`)**
~~3. **Content-Type aware**~~ ✅ **Resolved: Streaming vs non-streaming, not Accept header**
~~4. **Other clients affected?**~~ ✅ **Resolved: Skip, serve OpenCode first. Anthropic endpoint uses same error format.**
5. **Context overflow handling**~~ ✅ **Resolved: Must preserve `type: "context_length_exceeded"`, never overwrite with rate_limit code**

---

## Context Overflow Handling (Critical)

**OpenCode does NOT retry on context overflow** — it triggers compaction instead.

### OpenCode's Detection Order

```typescript
export function retryable(error) {
  // 1. FIRST: Check ContextOverflowError.isInstance() → NO retry, triggers compaction
  if (MessageV2.ContextOverflowError.isInstance(error)) return undefined
  
  // 2. THEN: Check isRetryable, JSON patterns (rate_limit, exhausted, etc.)
  ...
}
```

### Context OverflowError Patterns (from OpenCode source)

These are checked FIRST before any retry logic:

```json
{"code": "context_length_exceeded", "message": "..."}
{"message": "prompt is too long"}
{"message": "exceeds the context window"}
{"message": "maximum context length is 4096 tokens"}
{"message": "Input is too long for requested model"}
{"message": "reduce the length of the messages"}
```

### Proxy Responsibility

**Never** add `code: "rate_limit"` or `code: "exhausted"` to context overflow errors.

**Context overflow detection happens BEFORE JSON parsing** — OpenCode checks `ContextOverflowError.isInstance()` on the raw error object first. However, for safety and clarity:

| Error Type | `type` | `code` | Should have `rate_limit`? |
|------------|--------|--------|-------------------------|
| Context overflow | `context_length_exceeded` | - | ❌ NO |
| Rate limit | `rate_limit` | `rate_limit` | ✅ YES (after all models fail) |
| Provider exhausted | `error` | `exhausted` | N/A (already has code) |
| Service unavailable | `error` | `unavailable` | N/A (already has code) |

### New Format for Context Overflow

```json
{"type":"error","error":{"type":"context_length_exceeded","message":"Context window exceeded"}}
```

**Implementation requirement:** The proxy must detect context overflow errors from upstream and preserve their `type: "context_length_exceeded"` without overwriting with rate_limit codes.

---

## References

- OpenCode retry logic: `packages/opencode/src/session/retry.ts` (lines 61-100)
- Full OpenCode JSON reference: `/Users/nguyenminhkha/Downloads/opencode-dev/docs/proxy-retry-json-reference.md`
- Current proxy error handling: `pkg/proxy/handler.go`, `pkg/proxy/adapter_openai.go`
