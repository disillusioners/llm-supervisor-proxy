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
| Context overflow | `context_length_exceeded` | *(none)* | Should NOT trigger retry (OpenCode handles separately) |
| Authentication | `authentication_error` | *(none)* | Non-retryable |
| Generic upstream error | `upstream_error` | *(none)* | Default for unclassified errors |

### HTTP Status to Error Type Mapping

| HTTP Status | Error Type | Error Code |
|-------------|------------|------------|
| 400 | `upstream_error` | *(none)* |
| 401 | `authentication_error` | *(none)* |
| 403 | `authentication_error` | *(none)* |
| 429 | `rate_limit` | `rate_limit` (only after all models exhausted) |
| 502 | `upstream_error` | `unavailable` |
| 503 | `too_many_requests` | `unavailable` |
| 504 | `upstream_error` | *(none)* |
| Other 5xx | `upstream_error` | *(none)* |

**Note:** HTTP 503 is currently not handled separately in `race_coordinator.go:GetCommonFailureStatus()`. It falls through to the 502 case, producing `unavailable` without `type: "too_many_requests"`. Must add explicit 503 handling.

---

## Implementation Plan

### Phase 1: Define Error Format Constants and Helpers

**File:** `pkg/models/errors.go` (new)

```go
package models

import "strings"

// Error type constants (for error.type field)
const (
    ErrorTypeRateLimit           = "rate_limit"
    ErrorTypeTooManyRequests     = "too_many_requests"
    ErrorTypeAuthenticationError = "authentication_error"
    ErrorTypeContextOverflow     = "context_length_exceeded"
    ErrorTypeUpstreamError       = "upstream_error"
    ErrorTypeServerError         = "server_error"
)

// Error code constants (for error.code field - triggers OpenCode retry detection)
const (
    ErrorCodeRateLimit   = "rate_limit"
    ErrorCodeExhausted   = "exhausted"
    ErrorCodeUnavailable = "unavailable"
)

// OpenCodeErrorResponse is the OpenCode-compatible error format
type OpenCodeErrorResponse struct {
    Type  string       `json:"type"` // Always "error"
    Error ErrorDetails `json:"error"`
}

type ErrorDetails struct {
    Type    string `json:"type,omitempty"`
    Code    string `json:"code,omitempty"`
    Message string `json:"message"`
}

// NewOpenCodeError creates a new OpenCode-compatible error response
func NewOpenCodeError(errorType, code, message string) OpenCodeErrorResponse {
    return OpenCodeErrorResponse{
        Type: "error",
        Error: ErrorDetails{
            Type:    errorType,
            Code:    code,
            Message: message,
        },
    }
}

// IsContextOverflowError checks if an error indicates context window overflow.
// OpenCode checks these patterns BEFORE retry logic to trigger compaction instead.
func IsContextOverflowError(err error) bool {
    if err == nil {
        return false
    }
    msg := strings.ToLower(err.Error())
    patterns := []string{
        "context_length_exceeded",
        "prompt is too long",
        "exceeds the context window",
        "maximum context length",
        "input is too long",
        "reduce the length of the messages",
    }
    for _, p := range patterns {
        if strings.Contains(msg, p) {
            return true
        }
    }
    return false
}
```

### Phase 1b: Track HTTP Status in Race Requests

**File:** `pkg/proxy/race_request.go`

Add HTTP status code tracking to enable proper rate limit detection:

```go
type RaceRequest struct {
    // ... existing fields ...
    
    // HTTP status code from upstream (0 if not an HTTP error)
    httpStatusCode int
}

// SetHTTPStatus sets the HTTP status code from upstream
func (r *RaceRequest) SetHTTPStatus(code int) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.httpStatusCode = code
}

// GetHTTPStatus returns the HTTP status code
func (r *RaceRequest) GetHTTPStatus() int {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.httpStatusCode
}
```

**File:** `pkg/proxy/race_executor.go`

Update error handling to capture HTTP status codes when upstream returns errors.

### Phase 2: Update HTTP Error Responses (Non-Streaming)

**File:** `pkg/proxy/handler.go`

| Function | Current Format | New Format |
|----------|---------------|------------|
| `sendError()` | `{"error": {...}}` | `{"type":"error","error":{"type":"...","code":"...","message":"..."}}` |
| `sendAuthError()` | `{"error": "string", "message": "..."}` | `{"type":"error","error":{"type":"authentication_error","message":"..."}}` |
| `sendSSEError()` | `{"error": %q}` (quoted string) | `{"type":"error","error":{"type":"...","message":"..."}}` |
| HTTP 429 (after all models fail) | *(not implemented)* | `{"type":"error","error":{"type":"rate_limit","code":"rate_limit","message":"Rate limit exceeded"}}` |
| HTTP 503 | *(falls through to 502)* | `{"type":"error","error":{"type":"too_many_requests","code":"unavailable","message":"Service unavailable"}}` |

**Implementation notes:**
- `sendSSEError()` must produce proper JSON object, not quoted string (`%q`)
- `sendAuthError()` must convert error from string to object with `type: "authentication_error"`
- Omit `"code": nil` — use conditional struct or separate type for marshaling

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
// In buildFinalError() or similar function:

// Check if any model returned 429 (rate limit)
anyModel429 := false
for _, req := range failedRequests {
    if req.GetHTTPStatus() == 429 {
        anyModel429 = true
        break
    }
}

// Build base error from primary model's error
lastError := primaryRequest.GetError()
errorType := models.ErrorTypeUpstreamError
if anyModel429 {
    errorType = models.ErrorTypeRateLimit
}

// CRITICAL: Never add rate_limit code to context overflow errors
if models.IsContextOverflowError(lastError) {
    errorType = models.ErrorTypeContextOverflow
    // No code field - let OpenCode trigger compaction
    return models.NewOpenCodeError(errorType, "", lastError.Error())
}

// Add rate_limit code only if all models exhausted with 429
code := ""
if anyModel429 {
    code = models.ErrorCodeRateLimit
}

return models.NewOpenCodeError(errorType, code, lastError.Error())
```

**Important:** Skip adding `code: "rate_limit"` if the error is context overflow — preserve `type: "context_length_exceeded"` for OpenCode's compaction detection.

**Single model / Race retry disabled:** If race retry is disabled or only one model is configured, treat single model exhaustion as "all models exhausted" — add `code: "rate_limit"` when 429 occurred.

### Phase 5b: Stream Deadline Error Handling

**File:** `pkg/proxy/race_coordinator.go` (lines 416-427)

When `StreamDeadline` fires and buffer has no content:
```go
// Current: just closes channels, no error sent to client

// Fix: Send error response before closing
if req.buffer.Len() == 0 {
    // Send OpenCode-compatible error via event channel
    req.sendError(models.NewOpenCodeError(
        models.ErrorTypeUpstreamError,
        "",
        "Request timeout - no response received",
    ))
}
```

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

| File | Lines | Current State | Changes |
|------|-------|---------------|---------|
| `pkg/models/errors.go` | N/A | **Does not exist** | Create with error types, codes, structs, and `IsContextOverflowError()` helper |
| `pkg/proxy/handler.go` | 282-290, 293-304, 309-315, ~684, ~812 | Uses `map[string]interface{}` with `{"error":{...}}` format | Update `sendError()`, `sendAuthError()`, `sendSSEError()`, streaming and non-streaming error chunks |
| `pkg/proxy/adapter_openai.go` | 123-145 | `{"error":{"type":"...","message":"..."}}` | Add outer `type:"error"`, add `code` field support |
| `pkg/proxy/adapter_anthropic.go` | 180-207 | Already has `{"type":"error","error":{...}}` format ✅ | Add `code` field support only (format is already correct) |
| `pkg/proxy/race_coordinator.go` | 326-343, 512-553 | Collects errors as strings, no HTTP status tracking | Add HTTP status tracking, build OpenCode-compatible final error with rate_limit code |
| `pkg/proxy/race_request.go` | — | No HTTP status field | Add `httpStatusCode int` field with getter/setter |
| `pkg/proxy/race_executor.go` | 164, 468, 871 | Creates errors without HTTP status | Capture HTTP status code when upstream returns error |
| `pkg/ultimatemodel/handler.go` | 146-201 | `{"error":{"type":"...","code":"exhausted",...}}` | Add outer `type:"error"` wrapper |

## Testing Requirements

### Unit Tests

| Test File | Tests |
|-----------|-------|
| `pkg/models/errors_test.go` (new) | `NewOpenCodeError()` produces correct JSON format, `IsContextOverflowError()` detects all patterns |
| `pkg/proxy/handler_test.go` | `sendError()`, `sendAuthError()`, `sendSSEError()` produce OpenCode-compatible format |
| `pkg/proxy/adapter_openai_test.go` | `WriteError()`, `WriteStreamError()` include `type:"error"` at root |
| `pkg/proxy/adapter_anthropic_test.go` | `WriteError()`, `WriteStreamError()` include `event: error` prefix and `code` field |
| `pkg/proxy/race_request_test.go` | HTTP status tracking works correctly |
| `pkg/proxy/race_coordinator_test.go` | Rate limit code added only after all models fail, context overflow preserved |

### Integration Tests

| Test | Description |
|------|-------------|
| OpenCode retry simulation | Send error response, verify JSON matches patterns OpenCode's `retryable()` checks |
| End-to-end streaming test | Simulate stream with error, verify SSE format is parseable |
| Race retry exhausted test | Verify `code: "rate_limit"` added after all models fail with 429 |
| Context overflow preservation | Verify context overflow errors never get rate_limit code |

### Test Case Examples

```go
// pkg/models/errors_test.go

func TestOpenCodeErrorResponseJSON(t *testing.T) {
    err := models.NewOpenCodeError(
        models.ErrorTypeRateLimit,
        models.ErrorCodeRateLimit,
        "All models rate limited",
    )
    
    data, _ := json.Marshal(err)
    
    // Must have type:"error" at root
    assert.Contains(t, string(data), `"type":"error"`)
    // Must have error.type
    assert.Contains(t, string(data), `"type":"rate_limit"`)
    // Must have error.code for retry detection
    assert.Contains(t, string(data), `"code":"rate_limit"`)
}

func TestIsContextOverflowError(t *testing.T) {
    tests := []struct {
        err      error
        expected bool
    }{
        {fmt.Errorf("context_length_exceeded: max 4096"), true},
        {fmt.Errorf("prompt is too long"), true},
        {fmt.Errorf("exceeds the context window"), true},
        {fmt.Errorf("maximum context length is 4096 tokens"), true},
        {fmt.Errorf("Input is too long for requested model"), true},
        {fmt.Errorf("reduce the length of the messages"), true},
        {fmt.Errorf("rate limit exceeded"), false},
        {fmt.Errorf("service unavailable"), false},
    }
    
    for _, tt := range tests {
        assert.Equal(t, tt.expected, models.IsContextOverflowError(tt.err), tt.err.Error())
    }
}

func TestRateLimitCodeOnlyAfterAllModelsFail(t *testing.T) {
    // Scenario: Main model fails with 429, fallback succeeds
    // Expected: NO rate_limit code (fallback worked)
    
    // Scenario: Main and fallback both fail with 429
    // Expected: rate_limit code added
}
```

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

## Current State Analysis (Code Review Findings)

### Error Formats by File

| File | Location | Current Format | OpenCode-Compliant? |
|------|----------|----------------|---------------------|
| `pkg/proxy/handler.go` | `sendAuthError()` L282-290 | `{"error": "invalid_api_key", "message": "..."}` | ❌ No `type:"error"` |
| `pkg/proxy/handler.go` | `sendError()` L293-304 | `{"error": {"message": "...", "type": "..."}}` | ❌ No `type:"error"` at root |
| `pkg/proxy/handler.go` | `sendSSEError()` L309-315 | `event: error\ndata: {"error": "..."}\n\n` | ❌ No `type:"error"` |
| `pkg/proxy/handler.go` | Stream error ~L684 | `data: {"error": {"message": "...", "type": "..."}}\n\n` | ❌ No `type:"error"` at root |
| `pkg/proxy/adapter_openai.go` | `WriteError()` L123-132 | `{"error": {"type": "...", "message": "..."}}` | ❌ No `type:"error"` at root |
| `pkg/proxy/adapter_openai.go` | `WriteStreamError()` L134-145 | `data: {"error": {"type": "...", "message": "..."}}\n\n` | ❌ No `type:"error"` at root |
| `pkg/proxy/adapter_anthropic.go` | `WriteError()` L180-192 | `{"type": "error", "error": {"type": "...", "message": "..."}}` | ⚠️ Format correct, missing `code` |
| `pkg/proxy/adapter_anthropic.go` | `WriteStreamError()` L194-207 | `event: error\ndata: {"type": "error", "error": {...}}\n\n` | ⚠️ Format correct, missing `code` |
| `pkg/ultimatemodel/handler.go` | `SendRetryExhaustedError()` L161-171 | `{"error": {"type": "...", "code": "exhausted", ...}}` | ❌ No `type:"error"` at root |

### Key Findings

1. **Anthropic adapter is partially correct** — Already has `{"type":"error","error":{...}}` format. Only needs `code` field.

2. **No HTTP status tracking** — Race requests store errors as strings without HTTP status codes, making 429 detection difficult.

3. **No context overflow detection** — Currently no logic to detect and preserve context overflow errors differently from rate limits.

4. **Error types are strings, not constants** — Error types passed as string literals, risking typos. Need constants.

5. **Line numbers may drift** — Implementation should search by function name, not rely on exact line numbers.

---

## Additional Edge Cases Discovered During Review

The following issues were found during implementation planning that were not in the original plan:

### 1. `sendSSEError()` produces malformed JSON
**Location:** `handler.go:309-315`

Current code:
```go
fmt.Sprintf("event: error\ndata: {\"error\": %q}\n\n", message)
```

This produces: `event: error\ndata: {"error": "message text"}\n\n`

Should produce: `event: error\ndata: {"type":"error","error":{"message":"..."}}\n\n`

**Fix:** Convert to proper JSON object structure.

### 2. HTTP 503 handling missing
**Location:** `race_coordinator.go:539` (`GetCommonFailureStatus()`)

Plan maps 503 to `type: "too_many_requests"` but current code only checks:
- 429 → rate_limit
- 502 → unavailable  
- 413 → buffer_overflow
- 400 → invalid_request

503 falls through to default (502), producing wrong error type.

**Fix:** Add explicit 503 → `too_many_requests` mapping.

### 3. Race retry disabled scenario not implemented
**Location:** `race_coordinator.go`

Plan states: "If race retry is disabled or only one model is configured, rate limit code is still added (since 'all models' = the only model)."

This logic does not currently exist. When race retry is disabled:
- Only main model runs
- If it fails with 429, no "all models exhausted" state exists
- No rate_limit code gets added

**Fix:** Check if race retry is enabled. If disabled, treat single model failure as "all models exhausted".

### 4. `"code": nil` in `sendError()`
**Location:** `handler.go:301`

```go
"code": nil,  // Produces null in JSON
```

Should be omitted entirely when there's no code.

**Fix:** Use conditional marshaling or separate struct.

### 5. Stream deadline with no content doesn't send error
**Location:** `race_coordinator.go:416-427`

When `StreamDeadline` fires and buffer has no content, channels are closed without sending error response to client.

**Fix:** Send appropriate error response when closing due to timeout.

---

## References

- OpenCode retry logic: `packages/opencode/src/session/retry.ts` (lines 61-100)
- Full OpenCode JSON reference: `/Users/nguyenminhkha/Downloads/opencode-dev/docs/proxy-retry-json-reference.md`
- Current proxy error handling: `pkg/proxy/handler.go`, `pkg/proxy/adapter_openai.go`
