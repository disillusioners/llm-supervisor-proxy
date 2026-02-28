# Anthropic Protocol Proxy Implementation Plan

> **Status**: Oracle-reviewed, revised with recommendations

## Overview

Add a new endpoint `/v1/messages` to the proxy that accepts Anthropic Messages API requests and translates them to OpenAI Chat Completions format for upstream, then translates responses back to Anthropic format.

```
[Anthropic Client (Claude Code)] → [Proxy /v1/messages] → [Translate to OpenAI] → [OpenAI Upstream] → [Translate to Anthropic] → [Client]
```

### Key Architectural Decision: Batch Translation

**Critical**: The current proxy buffers ALL OpenAI SSE chunks until `[DONE]` for retry safety. We leverage this by translating the entire buffered response at flush time, NOT chunk-by-chunk.

```
OpenAI chunks → buffer (unchanged) → [DONE] → batch translate → flush Anthropic events
```

This simplifies state management and avoids complex streaming state across retries.

---

## 1. Architecture Overview

### 1.1 New Components

```
pkg/proxy/
├── handler.go                    # Existing: Add new HandleAnthropicMessages()
├── handler_anthropic.go          # NEW: Anthropic-specific request handling
├── translator/
│   ├── types.go                  # NEW: Shared types (AnthropicRequest, Response, Events)
│   ├── request.go                # NEW: Request translation (Anthropic → OpenAI)
│   ├── response.go               # NEW: Response translation (OpenAI → Anthropic)
│   ├── stream.go                 # NEW: Batch stream translation at flush time
│   ├── tools.go                  # NEW: Tool calling translation
│   └── errors.go                 # NEW: Error translation with status codes
```

### 1.2 Entry Point

In `cmd/main.go`, register the new endpoint:
```go
mux.HandleFunc("/v1/messages", proxyHandler.HandleAnthropicMessages)
```

### 1.3 Integration Pattern

```go
func (h *Handler) HandleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
    // 1. Extract API key (supports x-api-key, X-API-Key, Bearer)
    apiKey := h.extractAPIKey(r)
    
    // 2. Parse & validate Anthropic request
    var anthropicReq translator.AnthropicRequest
    if err := json.NewDecoder(r.Body).Decode(&anthropicReq); err != nil {
        h.sendAnthropicError(w, "invalid_request_error", err.Error(), 400)
        return
    }
    
    // 3. Translate to OpenAI format (includes model mapping)
    openaiReq := translator.TranslateRequest(&anthropicReq, modelMapping)
    
    // 4. Create requestContext with translation flag
    rc := h.initAnthropicRequestContext(r, openaiReq, anthropicReq)
    
    // 5. Reuse existing retry/fallback logic
    // Response translation happens at flush time based on rc.isAnthropic
}
```

---

## 2. Request Translation (Anthropic → OpenAI)

### 2.1 Endpoint & Auth

| Aspect | Anthropic | OpenAI |
|--------|-----------|--------|
| Endpoint | `/v1/messages` | `/v1/chat/completions` |
| Auth Header | `x-api-key: sk-ant-...` | `Authorization: Bearer sk-...` |
| Version Header | `anthropic-version: 2023-06-01` | (none) |

### 2.2 Request Body Translation

**Anthropic Request:**
```json
{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 1024,
  "system": "You are a helpful assistant.",
  "messages": [
    {"role": "user", "content": "Hello"}
  ],
  "stream": true
}
```

**OpenAI Request (translated):**
```json
{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 1024,
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"}
  ],
  "stream": true
}
```

### 2.3 Translation Rules

| Anthropic Field | OpenAI Mapping |
|----------------|----------------|
| `system` (top-level) | Insert as first message with `role: "system"` |
| `messages[].content` (text) | → `messages[].content` (string) |
| `messages[].content` (array with text/image) | → Flatten to OpenAI content array |
| `messages[].role` | Same (user/assistant) |
| `max_tokens` | Same |
| `stream` | Same |
| `temperature` | Same |
| `top_p` | Same |
| `stop_sequences` | → `stop` (array of strings) |
| `tools` | → `tools` (OpenAI function calling format) |

### 2.4 Multimodal Content Translation

**Anthropic Image:**
```json
{
  "role": "user",
  "content": [
    {"type": "text", "text": "What's in this?"},
    {
      "type": "image",
      "source": {
        "type": "base64",
        "media_type": "image/jpeg",
        "data": "base64..."
      }
    }
  ]
}
```

**OpenAI Image:**
```json
{
  "role": "user",
  "content": [
    {"type": "text", "text": "What's in this?"},
    {
      "type": "image_url",
      "image_url": {
        "url": "data:image/jpeg;base64,base64..."
      }
    }
  ]
}
```

### 2.5 Tool Calling Translation (CRITICAL)

Anthropic tool calling is fundamentally different from OpenAI. This is essential for Claude Code CLI compatibility.

**Anthropic tool_use (assistant message):**
```json
{
  "role": "assistant",
  "content": [
    {"type": "text", "text": "Let me check that"},
    {"type": "tool_use", "id": "toolu_01A", "name": "get_weather", "input": {"location": "SF"}}
  ]
}
```

**OpenAI equivalent:**
```json
{
  "role": "assistant",
  "content": "Let me check that",
  "tool_calls": [{
    "id": "toolu_01A",
    "type": "function",
    "function": {"name": "get_weather", "arguments": "{\"location\": \"SF\"}"}
  }]
}
```

**Anthropic tool_result (user message):**
```json
{
  "role": "user",
  "content": [
    {"type": "tool_result", "tool_use_id": "toolu_01A", "content": "72°F sunny"}
  ]
}
```

**OpenAI equivalent:**
```json
{
  "role": "tool",
  "tool_call_id": "toolu_01A",
  "content": "72°F sunny"
}
```

### 2.6 Model Mapping

Passing Anthropic model names to OpenAI upstream will fail. Add model mapping:

```go
type ModelMappingConfig struct {
    DefaultModel string            `json:"default_model"`
    Mapping      map[string]string `json:"mapping"` // claude-* -> openai model
}

// Example config:
{
  "default_model": "gpt-4o",
  "mapping": {
    "claude-sonnet-4-5-20250929": "gpt-4o",
    "claude-3-opus-20240229": "gpt-4-turbo",
    "claude-3-haiku-20240307": "gpt-4o-mini"
  }
}
```

---

## 3. Response Translation (OpenAI → Anthropic)

### 3.1 Non-Streaming Response

**OpenAI Response:**
```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "created": 1677652288,
  "model": "gpt-4o",
  "choices": [{
    "message": {"role": "assistant", "content": "Hello!"},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
}
```

**Anthropic Response (translated):**
```json
{
  "id": "msg_01XFDUDYJgAACzvnptvVoYEL",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Hello!"}],
  "model": "claude-sonnet-4-5-20250929",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 5}
}
```

### 3.2 Streaming Response Translation (Batch Approach)

**Key Insight**: The current proxy buffers ALL OpenAI chunks until `[DONE]` for retry safety. We translate the entire buffered response at flush time.

**Translation Flow:**
```
1. OpenAI chunks arrive → buffer unchanged (existing logic)
2. [DONE] received → trigger batch translation
3. Parse buffered OpenAI chunks
4. Generate complete Anthropic event sequence
5. Flush all Anthropic events to client
```

**OpenAI Buffered Chunks:**
```
data: {"id":"chatcmpl-123","choices":[{"delta":{"role":"assistant","content":"Hello"},"index":0}]}

data: {"id":"chatcmpl-123","choices":[{"delta":{"content":"!"},"index":0}]}

data: {"id":"chatcmpl-123","choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"completion_tokens":5}}

data: [DONE]
```

**Anthropic Events (generated at flush):**
```
event: message_start
data: {"type":"message_start","message":{"id":"msg_01XFDUDYJgAACzvnptvVoYEL","type":"message","role":"assistant","content":[]}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}
```

### 3.3 Translation Rules

| OpenAI Field | Anthropic Mapping |
|--------------|-------------------|
| `id: "chatcmpl-xxx"` | → `id: "msg_xxx"` (generate new) |
| `choices[].message.role` | → `role` |
| `choices[].message.content` | → `content: [{type: "text", text: ...}]` |
| `choices[].finish_reason` | → `stop_reason: "end_turn"` or `"max_tokens"` |
| `usage.prompt_tokens` | → `usage.input_tokens` |
| `usage.completion_tokens` | → `usage.output_tokens` |
| `model` | Same |

---

## 4. Implementation Steps

### Phase 1: Core Types (1 day)

Create `pkg/proxy/translator/types.go`:

```go
// Request types
type AnthropicRequest struct {
    Model       string                   `json:"model"`
    MaxTokens   int                      `json:"max_tokens"`
    System      interface{}              `json:"system"` // string or []ContentBlock
    Messages    []AnthropicMessage       `json:"messages"`
    Stream      bool                     `json:"stream,omitempty"`
    Temperature float64                  `json:"temperature,omitempty"`
    Tools       []AnthropicTool          `json:"tools,omitempty"`
    // ... other fields
}

type AnthropicMessage struct {
    Role    string        `json:"role"`
    Content interface{}   `json:"content"` // string or []ContentBlock
}

type ContentBlock struct {
    Type      string      `json:"type"` // "text", "image", "tool_use", "tool_result"
    Text      string      `json:"text,omitempty"`
    Source    *ImageSource `json:"source,omitempty"`
    // tool fields...
}

// Response types
type AnthropicResponse struct {
    ID      string        `json:"id"`
    Type    string        `json:"type"`
    Role    string        `json:"role"`
    Content []ContentBlock `json:"content"`
    Model   string        `json:"model"`
    StopReason string     `json:"stop_reason"`
    Usage   UsageInfo     `json:"usage"`
}

// Stream event types
type StreamEvent struct {
    Type string      `json:"type"`
    // type-specific fields...
}
```

### Phase 2: Request Translation + Tools (2 days)

Create `translator/request.go` and `translator/tools.go`:

**Key functions:**
```go
// request.go
func TranslateRequest(anthropic *AnthropicRequest, modelMapping ModelMappingConfig) map[string]interface{}
func translateSystem(system interface{}) []map[string]interface{}
func translateContent(content interface{}) interface{}

// tools.go
func TranslateToolUse(toolUse ContentBlock) map[string]interface{}
func TranslateToolResult(toolResult ContentBlock) map[string]interface{}
func TranslateToolsDefinition(tools []AnthropicTool) []map[string]interface{}
```

### Phase 3: Non-Streaming Response (1 day)

Create `translator/response.go`:

```go
func TranslateNonStreamResponse(openaiResp []byte, originalModel string) ([]byte, error)
func TranslateUsage(openaiUsage map[string]interface{}) UsageInfo
```

### Phase 4: Handler Skeleton (1 day)

Create `handler_anthropic.go`:

```go
func (h *Handler) HandleAnthropicMessages(w http.ResponseWriter, r *http.Request)
func (h *Handler) initAnthropicRequestContext(r *http.Request, openaiReq, anthropicReq) *requestContext
func (h *Handler) sendAnthropicError(w http.ResponseWriter, errorType, message string, statusCode int)
```

### Phase 5: Streaming Translation (3 days)

Create `translator/stream.go`:

```go
// Batch translation at flush time
func TranslateBufferedStream(openaiBuffer []byte, state *StreamState) ([]byte, error)

type StreamState struct {
    MessageID          string
    OriginalModel      string
    AccumulatedContent strings.Builder
    ThinkingContent    strings.Builder
    ToolCalls          []ToolCallState
    Usage              UsageInfo
    StopReason         string
}

func generateAnthropicEvents(state *StreamState) []string
```

### Phase 6: Integration (2 days)

1. Wire up with existing retry/fallback logic
2. Add model mapping config to `models.json`
3. End-to-end testing with Claude Code CLI

### Phase 7: Polish (1 day)

1. Error handling edge cases
2. Logging/observability
3. Documentation

---

## 5. Files to Create/Modify

### 5.1 Request ID Generation
```go
func generateAnthropicMessageID() string {
    return "msg_" + randomString(24)  // Anthropic format: msg_xxx
}
```

### 5.2 Stream Event Sequence

For streaming responses, maintain state across chunks:

1. **message_start** → Generate message ID, send event
2. **content_block_start** → When content begins, send event with index
3. **content_block_delta** → For each text delta, send event
4. **content_block_stop** → When content block ends
5. **message_delta** → When usage info available, send with stop_reason
6. **message_stop** → Final event

### 5.3 Error Translation

| OpenAI Error | Anthropic Error Format |
|--------------|----------------------|
| `{"error": {"message": "...", "type": "invalid_request_error"}}` | `{"type": "error","error": {"type": "invalid_request_error","message": "..."}}` |

---

## 6. Testing Plan

### 6.1 Unit Tests
- Request translation: `TestTranslateAnthropicToOpenAI`
- Response translation (non-stream): `TestTranslateNonStreamResponse`
- Response translation (stream): `TestTranslateStreamChunk`
- Multimodal content: `TestTranslateImageContent`

### 6.2 Integration Tests
- `/v1/messages` with non-streaming
- `/v1/messages?stream=true` with streaming
- Error handling (invalid request, auth failure)
- Model fallback behavior

### 6.3 Compatibility Tests
- Test with Claude Code CLI
- Test with official Anthropic SDK

---

## 5. Files to Create/Modify

### New Files
| File | Purpose | Est. Lines |
|------|---------|------------|
| `pkg/proxy/translator/types.go` | Shared types (Request, Response, Events) | ~150 |
| `pkg/proxy/translator/request.go` | Request translation (Anthropic → OpenAI) | ~200 |
| `pkg/proxy/translator/response.go` | Response translation (OpenAI → Anthropic) | ~150 |
| `pkg/proxy/translator/stream.go` | Batch stream translation | ~250 |
| `pkg/proxy/translator/tools.go` | Tool calling translation | ~200 |
| `pkg/proxy/translator/errors.go` | Error translation with status codes | ~100 |
| `pkg/proxy/translator/translator_test.go` | Unit tests for translation | ~300 |
| `pkg/proxy/handler_anthropic.go` | Anthropic endpoint handler | ~200 |
| `pkg/proxy/handler_anthropic_test.go` | Integration tests with mock server | ~400 |

### Modified Files
| File | Changes |
|------|---------|
| `cmd/main.go` | Register `/v1/messages` endpoint |
| `pkg/proxy/handler.go` | Add `HandleAnthropicMessages` method, lowercase `x-api-key` support |
| `pkg/proxy/handler_functions.go` | Add translation flag check at flush time |
| `pkg/proxy/handler_test.go` | Add `mockOpenAIHandler` helper (shared with Anthropic tests) |
| `pkg/config/models.json` | Add model mapping config |

---

## 6. Error Translation

### 6.1 HTTP Status Code Mapping

| HTTP Status | OpenAI Error | Anthropic Error Type |
|-------------|--------------|---------------------|
| 400 | `invalid_request_error` | `invalid_request_error` |
| 401 | `invalid_api_key` | `authentication_error` |
| 403 | - | `permission_error` |
| 404 | `model_not_found` | `not_found_error` |
| 429 | `rate_limit_exceeded` | `rate_limit_error` |
| 500 | - | `api_error` |
| 529 | - | `overloaded_error` |

### 6.2 Error Response Format

```go
func TranslateError(openaiError map[string]interface{}, statusCode int) []byte {
    anthropicError := map[string]interface{}{
        "type": "error",
        "error": map[string]interface{}{
            "type":    mapStatusCodeToErrorType(statusCode),
            "message": extractErrorMessage(openaiError),
        },
    }
    result, _ := json.Marshal(anthropicError)
    return result
}
```

---

## 7. Input Validation

```go
func ValidateAnthropicRequest(req *AnthropicRequest) error {
    if req.Model == "" {
        return errors.New("model is required")
    }
    if req.MaxTokens == 0 {
        return errors.New("max_tokens is required")
    }
    if len(req.Messages) == 0 {
        return errors.New("messages is required")
    }
    for _, msg := range req.Messages {
        if msg.Role != "user" && msg.Role != "assistant" {
            return fmt.Errorf("invalid role: %s", msg.Role)
        }
    }
    return nil
}
```

---

## 8. Auth Header Handling

Add lowercase `x-api-key` support to `extractAPIKey()`:

```go
func (h *Handler) extractAPIKey(r *http.Request) string {
    // Check Authorization: Bearer <token>
    authHeader := r.Header.Get("Authorization")
    if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
        return strings.TrimPrefix(authHeader, "Bearer ")
    }

    // Check X-API-Key (capitalized)
    if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
        return apiKey
    }

    // Check x-api-key (lowercase, Anthropic SDK uses this)
    if apiKey := r.Header.Get("x-api-key"); apiKey != "" {
        return apiKey
    }

    return ""
}
```

---

## 9. Edge Cases

| Edge Case | Impact | Handling |
|-----------|--------|----------|
| `system` as array | Medium | Flatten array to single string |
| Multiple `content` blocks | High | Translate each block (text + tool_use) |
| Thinking/reasoning blocks | Medium | Map `reasoning_content` → `thinking` block |
| Empty content | Medium | Allow empty assistant messages |
| Cache control | Low | Strip or pass through |
| `anthropic-version` header | Low | Validate but don't enforce |

---

## 10. Testing Plan

### 10.1 Mock Server Architecture

**Pattern**: Reuse existing `mockLLMHandler` pattern from `handler_test.go`. The mock server responds based on prompt keywords for deterministic testing.

```
┌─────────────────┐     ┌──────────────────────┐     ┌─────────────────┐
│  Test Case      │     │  Anthropic Handler   │     │  Mock OpenAI    │
│  (Anthropic     │ ──► │  (Translate Req)     │ ──► │  Server         │
│   Request)      │     │  (Translate Resp)    │ ◄── │  (keyword-based)│
└─────────────────┘     └──────────────────────┘     └─────────────────┘
```

### 10.2 Mock OpenAI Server for Anthropic Tests

Create `pkg/proxy/handler_anthropic_test.go`:

```go
// ─────────────────────────────────────────────────────────────────────────────
// Mock OpenAI Server for Anthropic Protocol Tests
// ─────────────────────────────────────────────────────────────────────────────

// mockOpenAIChunk creates an OpenAI SSE chunk
func mockOpenAIChunk(content string) string {
    chunk := map[string]interface{}{
        "id": "chatcmpl-test",
        "choices": []interface{}{
            map[string]interface{}{
                "delta": map[string]interface{}{
                    "content": content,
                },
                "index": 0,
            },
        },
    }
    b, _ := json.Marshal(chunk)
    return string(b)
}

// mockOpenAIToolCallChunk creates an OpenAI tool call chunk
func mockOpenAIToolCallChunk(id, name, args string) string {
    chunk := map[string]interface{}{
        "id": "chatcmpl-test",
        "choices": []interface{}{
            map[string]interface{}{
                "delta": map[string]interface{}{
                    "tool_calls": []interface{}{
                        map[string]interface{}{
                            "index": 0,
                            "id":    id,
                            "type":  "function",
                            "function": map[string]interface{}{
                                "name":      name,
                                "arguments": args,
                            },
                        },
                    },
                },
                "index": 0,
            },
        },
    }
    b, _ := json.Marshal(chunk)
    return string(b)
}

// mockOpenAIHandler creates a mock OpenAI server that responds based on keywords
// Keywords: mock-think, mock-tool, mock-500, mock-hang, mock-long
func mockOpenAIHandler(t *testing.T) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        reqBodyBytes, _ := io.ReadAll(r.Body)
        r.Body.Close()

        var reqBody map[string]interface{}
        json.Unmarshal(reqBodyBytes, &reqBody)

        isStream := true
        if s, ok := reqBody["stream"].(bool); ok && !s {
            isStream = false
        }

        // Extract prompt from messages
        var prompt string
        if msgs, ok := reqBody["messages"].([]interface{}); ok {
            for _, mb := range msgs {
                if msg, ok := mb.(map[string]interface{}); ok {
                    if content, ok := msg["content"].(string); ok {
                        prompt += content + " "
                    }
                }
            }
        }

        // Non-streaming response
        if !isStream {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusOK)
            resp := map[string]interface{}{
                "id":      "chatcmpl-test",
                "object":  "chat.completion",
                "created": time.Now().Unix(),
                "model":   "gpt-4o",
                "choices": []interface{}{
                    map[string]interface{}{
                        "index": 0,
                        "message": map[string]interface{}{
                            "role":    "assistant",
                            "content": "Hello! I am a helpful assistant.",
                        },
                        "finish_reason": "stop",
                    },
                },
                "usage": map[string]interface{}{
                    "prompt_tokens":     10,
                    "completion_tokens": 8,
                    "total_tokens":      18,
                },
            }
            json.NewEncoder(w).Encode(resp)
            return
        }

        // Handle special cases BEFORE setting headers
        if strings.Contains(prompt, "mock-500") {
            w.WriteHeader(http.StatusInternalServerError)
            fmt.Fprint(w, `{"error":{"message":"Internal error","type":"server_error"}}`)
            return
        }

        // Streaming response
        w.Header().Set("Content-Type", "text/event-stream")
        w.Header().Set("Cache-Control", "no-cache")
        w.WriteHeader(http.StatusOK)
        flusher := w.(http.Flusher)

        tokens := []string{"Hello", "!", " I", " am", " a", " helpful", " assistant", "."}

        if strings.Contains(prompt, "mock-think") {
            // Send reasoning_content then content
            thinkTokens := []string{"Hmm", ", ", "thinking", "..."}
            for _, t := range thinkTokens {
                chunk := map[string]interface{}{
                    "id": "chatcmpl-test",
                    "choices": []interface{}{
                        map[string]interface{}{
                            "delta": map[string]interface{}{"reasoning_content": t},
                            "index": 0,
                        },
                    },
                }
                b, _ := json.Marshal(chunk)
                fmt.Fprintf(w, "data: %s\n\n", string(b))
                flusher.Flush()
            }
            fmt.Fprintf(w, "data: %s\n\n", mockOpenAIChunk("Here is the answer."))
            flusher.Flush()
        } else if strings.Contains(prompt, "mock-tool") {
            // Send tool call
            fmt.Fprintf(w, "data: %s\n\n", mockOpenAIChunk("Let me check that."))
            flusher.Flush()
            fmt.Fprintf(w, "data: %s\n\n", mockOpenAIToolCallChunk("call_123", "get_weather", `{"location":"SF"}`))
            flusher.Flush()
        } else if strings.Contains(prompt, "mock-hang") {
            // Send some tokens then hang
            for i, token := range tokens {
                if i > 4 {
                    <-r.Context().Done()
                    return
                }
                fmt.Fprintf(w, "data: %s\n\n", mockOpenAIChunk(token))
                flusher.Flush()
            }
        } else {
            // Normal response
            for _, token := range tokens {
                fmt.Fprintf(w, "data: %s\n\n", mockOpenAIChunk(token))
                flusher.Flush()
            }
        }

        // Send final chunk with usage
        finalChunk := map[string]interface{}{
            "id": "chatcmpl-test",
            "choices": []interface{}{
                map[string]interface{}{
                    "delta":         map[string]interface{}{},
                    "index":         0,
                    "finish_reason": "stop",
                },
            },
            "usage": map[string]interface{}{
                "prompt_tokens":     10,
                "completion_tokens": 8,
            },
        }
        b, _ := json.Marshal(finalChunk)
        fmt.Fprintf(w, "data: %s\n\n", string(b))
        flusher.Flush()

        fmt.Fprintf(w, "data: [DONE]\n\n")
        flusher.Flush()
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// Test Helpers for Anthropic Endpoint
// ─────────────────────────────────────────────────────────────────────────────

// newAnthropicTestHandler creates a handler configured for Anthropic endpoint tests
func newAnthropicTestHandler(t *testing.T, upstreamHandler http.HandlerFunc) (*Handler, *httptest.Server) {
    t.Helper()
    upstream := httptest.NewServer(upstreamHandler)
    
    mgr := newTestManagerWithConfig(t, upstream.URL)
    cfg := &Config{
        ConfigMgr:    mgr,
        ModelsConfig: nil, // Use default
    }
    
    bus := events.NewBus()
    reqStore := store.NewRequestStore(100)
    h := NewHandler(cfg, bus, reqStore, nil, nil)
    
    t.Cleanup(func() { upstream.Close() })
    return h, upstream
}

// makeAnthropicRequest creates an Anthropic-style request
func makeAnthropicRequest(t *testing.T, body map[string]interface{}) *http.Request {
    t.Helper()
    b, _ := json.Marshal(body)
    req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("x-api-key", "test-key")
    req.Header.Set("anthropic-version", "2023-06-01")
    return req
}

// anthropicBody creates an Anthropic request body
func anthropicBody(model string, stream bool, messages []map[string]interface{}) map[string]interface{} {
    return map[string]interface{}{
        "model":     model,
        "max_tokens": 1024,
        "stream":    stream,
        "messages":  messages,
    }
}
```

### 10.3 Unit Tests

```go
// translator/translator_test.go

func TestTranslateAnthropicToOpenAI(t *testing.T) {
    tests := []struct {
        name       string
        input      translator.AnthropicRequest
        expected   map[string]interface{}
    }{
        {
            name: "simple text message",
            input: translator.AnthropicRequest{
                Model: "claude-sonnet-4-5",
                Messages: []translator.AnthropicMessage{
                    {Role: "user", Content: "Hello"},
                },
            },
            expected: map[string]interface{}{
                "model": "gpt-4o", // mapped
                "messages": []interface{}{
                    map[string]interface{}{"role": "user", "content": "Hello"},
                },
            },
        },
        {
            name: "system message conversion",
            input: translator.AnthropicRequest{
                Model:  "claude-sonnet-4-5",
                System: "You are helpful",
                Messages: []translator.AnthropicMessage{
                    {Role: "user", Content: "Hi"},
                },
            },
            expected: map[string]interface{}{
                "model": "gpt-4o",
                "messages": []interface{}{
                    map[string]interface{}{"role": "system", "content": "You are helpful"},
                    map[string]interface{}{"role": "user", "content": "Hi"},
                },
            },
        },
        // ... more cases
    }
    // ...
}

func TestTranslateToolUse(t *testing.T) {
    // Test tool_use → tool_calls conversion
}

func TestTranslateToolResult(t *testing.T) {
    // Test tool_result → role:tool conversion
}

func TestTranslateImageContent(t *testing.T) {
    // Test Anthropic image source → OpenAI image_url
}

func TestTranslateBufferedStream(t *testing.T) {
    // Test OpenAI chunks → Anthropic events
}

func TestTranslateError(t *testing.T) {
    // Test error translation with status codes
}
```

### 10.4 Integration Tests

```go
// handler_anthropic_test.go

func TestAnthropic_NonStreaming(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
        {"role": "user", "content": "Hello"},
    })
    req := makeAnthropicRequest(t, body)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rr.Code)
    }
    
    // Verify Anthropic response format
    var resp map[string]interface{}
    json.Unmarshal(rr.Body.Bytes(), &resp)
    
    if resp["type"] != "message" {
        t.Error("expected type 'message'")
    }
    if resp["role"] != "assistant" {
        t.Error("expected role 'assistant'")
    }
    if content, ok := resp["content"].([]interface{}); !ok || len(content) == 0 {
        t.Error("expected content array")
    }
}

func TestAnthropic_Streaming(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
        {"role": "user", "content": "Hello"},
    })
    req := makeAnthropicRequest(t, body)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rr.Code)
    }
    
    respBody := rr.Body.String()
    
    // Verify Anthropic SSE event format
    if !strings.Contains(respBody, "event: message_start") {
        t.Error("expected message_start event")
    }
    if !strings.Contains(respBody, "event: content_block_start") {
        t.Error("expected content_block_start event")
    }
    if !strings.Contains(respBody, "event: content_block_delta") {
        t.Error("expected content_block_delta event")
    }
    if !strings.Contains(respBody, "event: message_stop") {
        t.Error("expected message_stop event")
    }
}

func TestAnthropic_WithSystem(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    body := map[string]interface{}{
        "model":      "claude-sonnet-4-5",
        "max_tokens": 1024,
        "system":     "You are a pirate",
        "stream":     false,
        "messages": []map[string]interface{}{
            {"role": "user", "content": "Hello"},
        },
    }
    req := makeAnthropicRequest(t, body)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rr.Code)
    }
}

func TestAnthropic_ToolCall(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
        {"role": "user", "content": "mock-tool call"},
    })
    req := makeAnthropicRequest(t, body)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    // Verify tool_use content block in response
    respBody := rr.Body.String()
    if !strings.Contains(respBody, `"type":"tool_use"`) {
        t.Error("expected tool_use content block")
    }
}

func TestAnthropic_ThinkingStream(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
        {"role": "user", "content": "mock-think please"},
    })
    req := makeAnthropicRequest(t, body)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    // Verify thinking content block
    respBody := rr.Body.String()
    if !strings.Contains(respBody, `"type":"thinking"`) {
        t.Error("expected thinking content block")
    }
}

func TestAnthropic_Error500(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
        {"role": "user", "content": "mock-500 trigger"},
    })
    req := makeAnthropicRequest(t, body)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    // Verify Anthropic error format
    var resp map[string]interface{}
    json.Unmarshal(rr.Body.Bytes(), &resp)
    
    if resp["type"] != "error" {
        t.Error("expected type 'error'")
    }
}

func TestAnthropic_InvalidRequest(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    // Missing required fields
    body := map[string]interface{}{
        "model": "claude-sonnet-4-5",
        // missing max_tokens
        // missing messages
    }
    req := makeAnthropicRequest(t, body)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    if rr.Code != http.StatusBadRequest {
        t.Errorf("expected 400, got %d", rr.Code)
    }
}

func TestAnthropic_AuthWithXAPIKey(t *testing.T) {
    h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))
    
    body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
        {"role": "user", "content": "Hello"},
    })
    req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(mustMarshal(body)))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("x-api-key", "test-key") // lowercase
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    if rr.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", rr.Code)
    }
}
```

### 10.5 End-to-End Test with Real Protocol Flow

```go
// TestAnthropic_FullRoundTrip tests the complete translation cycle:
// Anthropic Request → OpenAI Request → OpenAI Response → Anthropic Response
func TestAnthropic_FullRoundTrip(t *testing.T) {
    var capturedOpenAIRequest map[string]interface{}
    
    // Mock that captures the translated OpenAI request
    captureHandler := func(w http.ResponseWriter, r *http.Request) {
        bodyBytes, _ := io.ReadAll(r.Body)
        json.Unmarshal(bodyBytes, &capturedOpenAIRequest)
        
        // Then respond with mock OpenAI response
        mockOpenAIHandler(t)(w, r)
    }
    
    h, _ := newAnthropicTestHandler(t, captureHandler)
    
    // Send Anthropic request
    anthropicReq := map[string]interface{}{
        "model":      "claude-sonnet-4-5",
        "max_tokens": 1024,
        "system":     "Be helpful",
        "stream":     false,
        "messages": []map[string]interface{}{
            {"role": "user", "content": "Hello"},
        },
    }
    req := makeAnthropicRequest(t, anthropicReq)
    rr := httptest.NewRecorder()
    
    h.HandleAnthropicMessages(rr, req)
    
    // Verify OpenAI request was translated correctly
    assert.Equal(t, "gpt-4o", capturedOpenAIRequest["model"]) // mapped
    
    openAIMsgs := capturedOpenAIRequest["messages"].([]interface{})
    assert.Equal(t, 2, len(openAIMsgs)) // system + user
    
    sysMsg := openAIMsgs[0].(map[string]interface{})
    assert.Equal(t, "system", sysMsg["role"])
    assert.Equal(t, "Be helpful", sysMsg["content"])
    
    userMsg := openAIMsgs[1].(map[string]interface{})
    assert.Equal(t, "user", userMsg["role"])
    
    // Verify Anthropic response format
    var resp map[string]interface{}
    json.Unmarshal(rr.Body.Bytes(), &resp)
    
    assert.Equal(t, "message", resp["type"])
    assert.Equal(t, "assistant", resp["role"])
}
```

### 10.6 Compatibility Tests

- Test with Claude Code CLI (`claude --api-base http://localhost:8080`)
- Test with official Anthropic SDK
- Test with `anthropic` Python package

---

## 11. Estimated Complexity (Revised)

| Component | Lines |
|-----------|-------|
| Types | ~150 |
| Request translation | ~200 |
| Response translation | ~150 |
| Stream translation | ~250 |
| Tool translation | ~200 |
| Error translation | ~100 |
| Handler | ~200 |
| Unit tests (translator) | ~300 |
| Integration tests (mock server) | ~400 |
| **Total** | **~1,950** |

**Estimated Time**: ~12 days (including comprehensive testing)

---

## 12. Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Tool calling incompatibility | High | Critical | Implement full tool translation |
| Streaming state bugs | Medium | High | Use batch translation approach |
| Model name mismatches | High | Medium | Add model mapping config |
| Auth header differences | Medium | Medium | Add lowercase x-api-key support |
| Edge case misses | Medium | Medium | Test with Claude Code CLI extensively |
| Performance regression | Low | Low | Benchmark before/after |
