## Task: Fix /v1/messages for Claude Code Compatibility

### Context
Claude Code sends requests to `/v1/messages` in Anthropic Messages API format. The proxy translates these to OpenAI format and forwards to OpenAI-compatible upstreams. Several issues prevent Claude Code from working correctly.

### Task 1: Add missing fields to types

**File:** `pkg/proxy/translator/types.go`

**Changes:**
1. Add `Thinking` field to `AnthropicRequest` struct (after `Metadata`):
```go
Thinking *ThinkingConfig `json:"thinking,omitempty"`
```

2. Add `ThinkingConfig` struct:
```go
type ThinkingConfig struct {
    Type         string `json:"type"`           // "enabled"
    BudgetTokens int    `json:"budget_tokens"`
}
```

3. Add cache token fields to `UsageInfo` struct:
```go
CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
```

**Why:** Claude Code sends `thinking` param for extended thinking. Without this field, it's silently dropped during JSON unmarshal. Cache tokens are needed for accurate usage reporting.

### Task 2: Fix copyAnthropicHeaders — forward Anthropic headers

**File:** `pkg/proxy/handler_anthropic.go`

**Changes to `copyAnthropicHeaders` function (~line 636):**
- Currently `anthropic-version` is in the skip/continue case alongside `x-api-key`
- Remove `anthropic-version` from that case — let it fall through to the generic forwarding loop
- `anthropic-beta` is not explicitly handled, so it should already be forwarded — verify this
- Keep `x-api-key` → `Authorization: Bearer` conversion (upstream is OpenAI)

**Before:**
```go
case "x-api-key", "anthropic-version":
    if strings.ToLower(name) == "x-api-key" && len(values) > 0 {
        dst.Header.Set("Authorization", "Bearer "+values[0])
    }
    continue
```

**After:**
```go
case "x-api-key":
    if len(values) > 0 {
        dst.Header.Set("Authorization", "Bearer "+values[0])
    }
    continue
```

### Task 3: Fix validation — don't reject complex content blocks

**File:** `pkg/proxy/handler_anthropic.go`

**Changes to `validateAnthropicRequest` function (~line 624):**
- Current validation only checks role and basic fields — this is already correct
- The issue is that `content` can be an array of content blocks (tool_use, tool_result, thinking) — but the current validation doesn't inspect content, so it should pass
- Verify by checking if `json.Unmarshal` into `AnthropicRequest` handles array content correctly
- If content is `[]interface{}` with tool_use/tool_result/thinking blocks, ensure the translator can process them

**Actually — after review, validation only checks role and required fields. Content blocks are handled by the translator. This task may be a no-op if validation already passes. Dev should verify and report.**

### Constraints
- All changes in existing files only (types.go, handler_anthropic.go)
- No new files, no new dependencies
- Must not break existing OpenAI translation path
- Must not break existing tests

### Test
After all tasks, test with:
```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "x-api-key: test" \
  -H "anthropic-version: 2023-06-01" \
  -H "anthropic-beta: prompt-caching-2024-07-31" \
  -H "content-type: application/json" \
  -d '{"model":"test","max_tokens":100,"messages":[{"role":"user","content":"Hi"}],"thinking":{"type":"enabled","budget_tokens":5000}}'
```
