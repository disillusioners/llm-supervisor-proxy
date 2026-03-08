# Architecture Refactor: Unified Proxy Handler

## Problem

Anthropic endpoint didn't show messages in frontend while OpenAI endpoint worked correctly.

**Root cause**: Duplicated handler logic (~1856 lines) with inconsistent behavior.

## Streaming Architecture

> **Important**: This proxy uses **buffer-then-flush** streaming, NOT word-by-word streaming.

```
Upstream chunks → Buffer (accumulate) → [DONE] → Flush to client
```

Both handlers (`handleStreamResponse` and `handleAnthropicStreamResponse`) buffer ALL chunks from upstream and only write to the client after receiving `[DONE]`. This means:

- **No per-chunk streaming** to client
- **No complex state management** needed in adapters
- **Simple flow**: buffer → translate (if needed) → write

## Target Architecture

```
┌──────────────────────────────────────────────┐
│            Unified Proxy Handler              │
│  - Single request flow                        │
│  - Buffer-then-flush for streaming            │
│  - finalizeSuccess() called once              │
└───────────────────┬──────────────────────────┘
                    │
      ┌─────────────┴─────────────┐
      ▼                           ▼
┌──────────────┐          ┌──────────────┐
│ OpenAI       │          │ Anthropic    │
│ Adapter      │          │ Adapter      │
│ (passthrough)│          │ (translate)  │
└──────────────┘          └──────────────┘
      │                           │
      └───────────┬───────────────┘
                  ▼
           OpenAI Upstream
```

---

## Completed ✅

### Bug Fix (handler_anthropic.go)
- [x] Added response tracking fields to `anthropicRequestContext`
- [x] Added `finalizeAnthropicSuccess()` method
- [x] Added `extractOpenAIResponseContent()` helper
- [x] Updated all 4 response handlers to track and store assistant message

### Adapter Foundation (NEW files)
- [x] `pkg/proxy/adapter.go` - ProtocolAdapter interface + ResponseState
- [x] `pkg/proxy/adapter_openai.go` - OpenAI passthrough adapter
- [x] `pkg/proxy/adapter_anthropic.go` - Anthropic translation adapter
- [x] `pkg/proxy/adapter_helpers.go` - Shared helper functions

---

---

## Remaining Work 📋

### Phase 1: Fix Adapter Interface (1-2 days) ✅ DONE

**Problem**: Current `ResponseWriter` interface has `WriteStreamEvent()` which assumes per-chunk streaming, but our architecture is buffer-then-flush.

- [x] Replace `WriteStreamEvent()` with `WriteBufferedStream()` in interface
- [x] Update `OpenAIAdapter.WriteBufferedStream()` - passthrough
- [x] Update `AnthropicAdapter.WriteBufferedStream()` - call `translator.TranslateBufferedStream()`
- [ ] Fix `getAnthropicModelMapping()` - currently returns empty config, ignores parameter

### Phase 2: Create Unified Handler (2-3 days)

- [ ] Create `pkg/proxy/handler_unified.go` with `HandleWithAdapter()`
- [ ] Unified flow:
  ```
  parse → translate → upstream → buffer → translate → flush → finalize
  ```
- [ ] Single `finalizeSuccess()` for both protocols
- [ ] Handle both streaming (buffer-then-flush) and non-streaming

### Phase 3: Migrate OpenAI Endpoint (2-3 days)

- [ ] Update `HandleChatCompletions` to use OpenAI adapter
- [ ] Verify existing tests pass
- [ ] A/B testing: route 10% traffic to new handler
- [ ] Monitor: latency, error rates

### Phase 4: Migrate Anthropic Endpoint (2-3 days)

- [ ] Update `HandleAnthropicMessages` to use Anthropic adapter
- [ ] Verify frontend displays messages correctly (original bug)
- [ ] A/B testing: 10% → 50% → 100%
- [ ] Test response tracking end-to-end

### Phase 5: Cleanup (1-2 days)

- [ ] Consolidate `requestContext` and `anthropicRequestContext` into single type
- [ ] Remove duplicated code from `handler_functions.go`
- [ ] Remove duplicated code from `handler_anthropic.go`
- [ ] Update documentation

---

## Key Files

| File | Status | Purpose |
|------|--------|---------|
| `adapter.go` | ✅ Fixed | Interface definitions |
| `adapter_openai.go` | ✅ Fixed | OpenAI passthrough |
| `adapter_anthropic.go` | ✅ Fixed | Anthropic translation |
| `handler_unified.go` | 📋 TODO | Single handler flow |
| `handler_functions.go` | 🔧 Refactor | Remove duplication |
| `handler_anthropic.go` | 🔧 Refactor | Remove duplication |

---

## Interface Changes

### Before (Current - Wrong)
```go
type ResponseWriter interface {
    WriteNonStreamResponse(w http.ResponseWriter, openaiResponse []byte) error
    WriteStreamEvent(w http.ResponseWriter, openaiChunk []byte) error  // ❌ Per-chunk
    WriteStreamDone(w http.ResponseWriter) error
    SetStreamHeaders(w http.ResponseWriter)
}
```

### After (Correct for buffer-then-flush)
```go
type ResponseWriter interface {
    WriteNonStreamResponse(w http.ResponseWriter, openaiResponse []byte) error
    WriteBufferedStream(w http.ResponseWriter, openaiBuffer []byte) error  // ✅ Full buffer
    SetStreamHeaders(w http.ResponseWriter)
}
```

---

**Note**: 5 pre-existing test failures discovered (unrelated to adapter interface changes):
- `TestAnthropic_ThinkingStream`
- `TestInvalidJSON`
- `TestMockLLM_ToolCall`
- `TestProviderSpecificThinking`
- `TestFallback4xxTriggered`

These failures existed before Phase 1 changes and should be investigated separately.

---

## Known Issues to Fix

### 1. Model Mapping Bug
**Location**: `adapter_anthropic.go:258-267`
```go
func getAnthropicModelMapping(_ models.ModelsConfigInterface) *translator.ModelMappingConfig {
    return &translator.ModelMappingConfig{
        // No DefaultModel - let unknown models pass through
    }
}
```
**Fix**: Actually use the config parameter to extract mappings.

### 2. Context Duplication
- `requestContext` (43 fields) in `handler_helpers.go`
- `anthropicRequestContext` (22 fields) in `handler_anthropic.go`
- ~80% overlap → should consolidate into single `unifiedRequestContext`

---

## Timeline Estimate

| Phase | Days | Risk |
|-------|------|------|
| Phase 1: Fix Interface | 1-2 | Low |
| Phase 2: Unified Handler | 2-3 | Medium |
| Phase 3: Migrate OpenAI | 2-3 | Low |
| Phase 4: Migrate Anthropic | 2-3 | Medium |
| Phase 5: Cleanup | 1-2 | Low |
| **Total** | **8-13** | - |

---

## Benefits

1. **Bug prevention** - One code path = consistent behavior
2. **Easier maintenance** - Change logic in one place
3. **Extensibility** - Add new protocols by implementing ProtocolAdapter
4. **Testability** - Test adapter translations independently
5. **Simplicity** - Buffer-then-flush means no complex streaming state

---

## Rollback Strategy

- Feature flags for each endpoint
- Keep old handlers for 2 weeks post-migration
- Monitoring dashboards for error rates, latency
