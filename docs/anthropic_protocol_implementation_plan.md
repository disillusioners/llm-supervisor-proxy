# Anthropic Protocol Proxy Implementation

> **Status**: ✅ Implemented (Feb 2026)

## Overview

The proxy now supports both OpenAI and Anthropic APIs:

```
[Client] → /v1/chat/completions → [OpenAI format] → Upstream
[Client] → /v1/messages         → [Translate] → [OpenAI format] → Upstream
```

## Files Created

| File | Purpose | Lines |
|------|---------|-------|
| `pkg/proxy/translator/types.go` | Shared types (Request, Response, Events) | ~200 |
| `pkg/proxy/translator/request.go` | Anthropic → OpenAI translation | ~400 |
| `pkg/proxy/translator/response.go` | OpenAI → Anthropic translation | ~180 |
| `pkg/proxy/translator/stream.go` | Batch stream translation | ~260 |
| `pkg/proxy/translator/tools.go` | Tool calling translation | ~220 |
| `pkg/proxy/translator/errors.go` | Error translation | ~100 |
| `pkg/proxy/handler_anthropic.go` | `/v1/messages` handler | ~390 |
| `pkg/proxy/translator/translator_test.go` | Unit tests | ~370 |
| `pkg/proxy/handler_anthropic_test.go` | Integration tests | ~540 |

## Files Modified

| File | Change |
|------|--------|
| `pkg/proxy/handler.go` | Added lowercase `x-api-key` header support |
| `cmd/main.go` | Registered `/v1/messages` endpoint |

## Translation Rules

### Request (Anthropic → OpenAI)

| Anthropic | OpenAI |
|-----------|--------|
| `system` (top-level) | `messages[].role: "system"` |
| `messages[].content` (string) | Same |
| `messages[].content` (array) | Flatten to content parts |
| `stop_sequences` | `stop` |
| `tools[].input_schema` | `tools[].function.parameters` |

### Response (OpenAI → Anthropic)

| OpenAI | Anthropic |
|--------|-----------|
| `id: "chatcmpl-xxx"` | `id: "msg_xxx"` (new ID) |
| `choices[].message.content` | `content: [{type: "text", text: ...}]` |
| `reasoning_content` | `content: [{type: "thinking", ...}]` |
| `tool_calls` | `content: [{type: "tool_use", ...}]` |
| `finish_reason: "stop"` | `stop_reason: "end_turn"` |
| `prompt_tokens` | `input_tokens` |
| `completion_tokens` | `output_tokens` |

### Streaming

Batch translation at `[DONE]`:
1. Buffer OpenAI chunks unchanged
2. On `[DONE]`, translate entire buffer
3. Emit Anthropic SSE events: `message_start` → `content_block_start` → `content_block_delta` → `content_block_stop` → `message_delta` → `message_stop`

### Error Translation

| HTTP Status | Anthropic Error Type |
|-------------|---------------------|
| 400 | `invalid_request_error` |
| 401 | `authentication_error` |
| 403 | `permission_error` |
| 404 | `not_found_error` |
| 429 | `rate_limit_error` |
| 500 | `api_error` |
| 529 | `overloaded_error` |

## Model Mapping

Default mapping (configurable):

```go
"claude-sonnet-4-5-20250929": "gpt-4o",
"claude-3-opus-20240229":     "gpt-4-turbo",
"claude-3-haiku-20240307":    "gpt-4o-mini",
```

## Tests

```bash
# Unit tests
go test ./pkg/proxy/translator/... -v

# Integration tests
go test ./pkg/proxy/... -run TestAnthropic -v
```

## Usage

```bash
# OpenAI endpoint (unchanged)
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer key" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}'

# Anthropic endpoint (new)
curl http://localhost:8080/v1/messages \
  -H "x-api-key: key" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4-5","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}]}'
```
