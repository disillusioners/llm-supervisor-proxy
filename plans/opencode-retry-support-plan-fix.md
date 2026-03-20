# Fix Plan: Switch Back to OpenAI Error Format for OpenCode Retry

## Problem

The original plan ([`opencode-retry-support-plan.md`](opencode-retry-support-plan.md)) made TWO mistakes:

### Mistake 1: Wrong OpenAI Format
Implemented `{"type":"error","error":{...}}` when OpenCode expects OpenAI format `{"error":{...}}`.

### Mistake 2: Wrong Anthropic SSE Error Format
Implemented custom `event: error` which is NOT part of the official Anthropic protocol.

**Anthropic Protocol Reality:**
- Most errors → returned as HTTP error response (non-200) before streaming starts
- If error during streaming → send `event: message_stop` or just close the stream
- Official Anthropic events: `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`
- **NO official `event: error` exists**

---

## Scope of Changes

| File | Current (Wrong) | Fixed (Correct) |
|------|-----------------|-----------------|
| `pkg/models/errors.go` | `OpenCodeErrorResponse` with `Type` at root | `OpenAIErrorResponse` with only `Error` field |
| `pkg/models/errors_test.go` | Tests check for `"type":"error"` at root | Tests check for `{"error":{...}}` |
| `pkg/proxy/handler.go` | Uses `NewOpenCodeError()` | Use `NewOpenAIError()` |
| `pkg/proxy/adapter_openai.go` | `{"type":"error","error":{...}}` | `{"error":{...}}` |
| `pkg/proxy/adapter_anthropic.go` | `event: error` (non-standard) | HTTP error before stream OR `event: message_stop` |
| `test/test_mock_race_retry_internal_path.sh` | Checks for `"type":"error"` at root | Check for `{"error":{...}}` |
| `test/test_mock_race_retry_external_path.sh` | Checks for `"type":"error"` at root | Check for `{"error":{...}}` |

---

## Correct Formats by Endpoint

### OpenAI Endpoint

**Non-streaming error:**
```json
{
  "error": {
    "type": "rate_limit_error",
    "code": "rate_limit",
    "message": "All models rate limited"
  }
}
```

**Streaming error (inside stream):**
```
data: {"error":{"type":"server_error","message":"Streaming error: ..."}}

```

### Anthropic Endpoint

**Option A: HTTP error before streaming (recommended)**
```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json

{
  "type": "error",
  "error": {
    "type": "rate_limit_error",
    "message": "Rate limit exceeded"
  }
}
```

**Option B: During streaming (if must send in stream)**
```
event: message_stop
data: {}

```
Then close the stream.

**DO NOT use:** `event: error` (not part of Anthropic protocol)

---

## OpenCode Retry Detection (Actual Behavior)

OpenCode's `SessionRetry.retryable()` checks these patterns in the **OpenAI format**:

| Pattern | Triggers Retry | Notes |
|---------|---------------|-------|
| `json.error?.type === "too_many_requests"` | ✅ | HTTP 429 |
| `json.error?.code?.includes("rate_limit")` | ✅ | Rate limit code |
| `json.error?.code?.includes("exhausted")` | ✅ | Provider exhausted |
| `json.error?.code?.includes("unavailable")` | ✅ | Service unavailable |

### Context Overflow Detection

| Pattern | Triggers Compaction | Notes |
|---------|---------------------|-------|
| `json.error?.code === "context_length_exceeded"` | ✅ | No retry, triggers compaction |
| `json.error?.message?.includes("prompt is too long")` | ✅ | Message-based detection |

---

## Implementation Plan

### Phase 1: Fix `pkg/models/errors.go`

**Current (Wrong):**
```go
type OpenCodeErrorResponse struct {
    Type  string       `json:"type"` // Always "error"
    Error ErrorDetails `json:"error"`
}
```

**Fixed:**
```go
// OpenAIErrorResponse is the OpenAI-compatible error format
// Used for OpenAI endpoint responses
type OpenAIErrorResponse struct {
    Error ErrorDetails `json:"error"`
}

// AnthropicErrorResponse is the Anthropic-compatible error format
// Used for Anthropic endpoint responses (HTTP errors, not SSE)
type AnthropicErrorResponse struct {
    Type  string       `json:"type"` // Always "error"
    Error ErrorDetails `json:"error"`
}

type ErrorDetails struct {
    Type    string `json:"type,omitempty"`
    Code    string `json:"code,omitempty"`
    Message string `json:"message"`
}

// NewOpenAIError creates a new OpenAI-compatible error response
func NewOpenAIError(errorType, code, message string) OpenAIErrorResponse {
    return OpenAIErrorResponse{
        Error: ErrorDetails{
            Type:    errorType,
            Code:    code,
            Message: message,
        },
    }
}

// NewAnthropicError creates a new Anthropic-compatible error response
func NewAnthropicError(errorType, code, message string) AnthropicErrorResponse {
    return AnthropicErrorResponse{
        Type: "error",
        Error: ErrorDetails{
            Type:    errorType,
            Code:    code,
            Message: message,
        },
    }
}

// Backward compatibility aliases
var NewOpenCodeError = NewOpenAIError
type OpenCodeErrorResponse = OpenAIErrorResponse
```

### Phase 2: Fix `pkg/models/errors_test.go`

**Update tests to verify correct formats:**

```go
func TestOpenAIErrorResponseJSON(t *testing.T) {
    err := NewOpenAIError(ErrorTypeRateLimit, ErrorCodeRateLimit, "test")
    data, _ := json.Marshal(err)
    jsonStr := string(data)
    
    // OpenAI format: NO type at root
    assert.NotContains(t, jsonStr, `"type":"error"`)
    assert.Contains(t, jsonStr, `"error":{`)
    assert.Contains(t, jsonStr, `"code":"rate_limit"`)
}

func TestAnthropicErrorResponseJSON(t *testing.T) {
    err := NewAnthropicError(ErrorTypeRateLimit, ErrorCodeRateLimit, "test")
    data, _ := json.Marshal(err)
    jsonStr := string(data)
    
    // Anthropic format: HAS type at root
    assert.Contains(t, jsonStr, `"type":"error"`)
    assert.Contains(t, jsonStr, `"error":{`)
}
```

### Phase 3: Fix `pkg/proxy/adapter_openai.go`

**Remove `"type": "error"` from root level:**

```go
func (a *OpenAIAdapter) WriteError(w http.ResponseWriter, errorType, message string, statusCode int) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    json.NewEncoder(w).Encode(map[string]interface{}{
        "error": map[string]interface{}{
            "type":    errorType,
            "message": message,
        },
    })
}

func (a *OpenAIAdapter) WriteStreamErrorWithCode(w http.ResponseWriter, errorType, code, message string) {
    errorResp := map[string]interface{}{
        "error": map[string]interface{}{
            "type":    errorType,
            "message": message,
        },
    }
    if code != "" {
        errorResp["error"].(map[string]interface{})["code"] = code
    }
    eventBytes, _ := json.Marshal(errorResp)
    fmt.Fprintf(w, "data: %s\n\n", string(eventBytes))
    if f, ok := w.(http.Flusher); ok {
        f.Flush()
    }
}

func (a *OpenAIAdapter) WriteErrorWithCode(w http.ResponseWriter, errorType, code, message string, statusCode int) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    errorResp := map[string]interface{}{
        "error": map[string]interface{}{
            "type":    errorType,
            "message": message,
        },
    }
    if code != "" {
        errorResp["error"].(map[string]interface{})["code"] = code
    }
    json.NewEncoder(w).Encode(errorResp)
}
```

### Phase 4: Fix `pkg/proxy/adapter_anthropic.go`

**For non-streaming errors (HTTP response):**
```go
func (a *AnthropicAdapter) WriteError(w http.ResponseWriter, errorType, message string, statusCode int) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    // Anthropic format has "type": "error" at root
    json.NewEncoder(w).Encode(map[string]interface{}{
        "type": "error",
        "error": map[string]interface{}{
            "type":    errorType,
            "message": message,
        },
    })
}
```

**For streaming errors (during SSE stream):**
```go
func (a *AnthropicAdapter) WriteStreamErrorWithCode(w http.ResponseWriter, errorType, code, message string) {
    // Option A: Send message_stop and close (Anthropic-compliant)
    fmt.Fprintf(w, "event: message_stop\ndata: {}\n\n")
    if f, ok := w.(http.Flusher); ok {
        f.Flush()
    }
    // Stream will be closed by handler
    
    // Note: We cannot send error details in-stream with Anthropic protocol.
    // The client should check for abrupt stream termination.
    // For detailed errors, return HTTP error before streaming starts.
}
```

**Better approach for streaming errors:**
The handler should detect errors BEFORE starting the SSE stream and return HTTP error response instead.

### Phase 5: Fix `pkg/proxy/handler.go`

Update to use correct error format based on adapter protocol:

```go
func (h *Handler) sendError(w http.ResponseWriter, errType, errorCode, message string, code int) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    
    // Use adapter to write error in correct format
    if h.adapter != nil && h.adapter.Protocol() == "anthropic" {
        json.NewEncoder(w).Encode(models.NewAnthropicError(errType, errorCode, message))
    } else {
        json.NewEncoder(w).Encode(models.NewOpenAIError(errType, errorCode, message))
    }
}
```

### Phase 6: Fix Test Scripts

**File:** `test/test_mock_race_retry_internal_path.sh`

```bash
# Check for OpenAI error format
if echo "$response" | grep -q '"error":{'; then
    echo -e "${GREEN}✓ Has \"error\" object at root (OpenAI format)${NC}"
else
    echo -e "${RED}✗ Missing \"error\" object at root${NC}"
    success=false
fi

# Should NOT have type:"error" at root for OpenAI endpoint
if echo "$response" | grep -q '"type":"error"'; then
    echo -e "${RED}✗ Has wrong \"type\":\"error\" at root (Anthropic format)${NC}"
    success=false
fi
```

---

## Files to Modify Summary

| File | Lines | Change Type |
|------|-------|-------------|
| `pkg/models/errors.go` | 22-43 | Add separate `OpenAIErrorResponse` and `AnthropicErrorResponse` types |
| `pkg/models/errors_test.go` | All | Update tests for both formats |
| `pkg/proxy/handler.go` | 286-297, 304-305, 684-685, 814-815 | Use correct format based on adapter protocol |
| `pkg/proxy/adapter_openai.go` | 123-173 | Remove `"type": "error"` from root |
| `pkg/proxy/adapter_anthropic.go` | 194-215 | Replace `event: error` with `event: message_stop` or HTTP error |
| `test/test_mock_race_retry_internal_path.sh` | 241-247 | Change grep patterns |
| `test/test_mock_race_retry_external_path.sh` | 191-197 | Change grep patterns |

---

## Testing Requirements

### Unit Tests

```go
func TestOpenAIFormat(t *testing.T) {
    // OpenAI format: {"error":{...}}
    err := models.NewOpenAIError("rate_limit", "rate_limit", "test")
    data, _ := json.Marshal(err)
    assert.NotContains(t, string(data), `"type":"error"`)
    assert.Contains(t, string(data), `"error":{`)
}

func TestAnthropicFormat(t *testing.T) {
    // Anthropic format: {"type":"error","error":{...}}
    err := models.NewAnthropicError("rate_limit", "", "test")
    data, _ := json.Marshal(err)
    assert.Contains(t, string(data), `"type":"error"`)
    assert.Contains(t, string(data), `"error":{`)
}
```

### Integration Tests

| Endpoint | Error Type | Expected Format |
|----------|------------|-----------------|
| OpenAI | Non-streaming | `{"error":{"type":"...","message":"..."}}` |
| OpenAI | Streaming | `data: {"error":{"type":"...","message":"..."}}` |
| Anthropic | Non-streaming | `{"type":"error","error":{"type":"...","message":"..."}}` |
| Anthropic | Streaming | `event: message_stop` + close stream |

---

## Summary

**Two fixes needed:**

1. **OpenAI Endpoint:** Use `{"error":{...}}` format (no `type` at root)
2. **Anthropic Endpoint:** 
   - Non-streaming: Use `{"type":"error","error":{...}}` format
   - Streaming: Use `event: message_stop` then close (NOT custom `event: error`)

The key insight is that OpenCode receives OpenAI format directly, while Anthropic has its own protocol that doesn't include `event: error`.
