# Architecture Refactor: Unified Proxy Handler

## Problem

Anthropic endpoint didn't show messages in frontend while OpenAI endpoint worked correctly.

**Root cause**: Duplicated handler logic (~1700 lines) with inconsistent behavior.

## Target Architecture

```
┌──────────────────────────────────────────────┐
│            Unified Proxy Handler              │
│  - Single request flow                        │
│  - Shared state tracking (ResponseState)      │
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

## Remaining Work 📋

### Phase 1: Create Unified Handler
- [ ] Create `pkg/proxy/handler_unified.go` with `HandleWithAdapter()`
- [ ] Unified flow: parse → translate → upstream → translate → finalize
- [ ] Single `finalizeSuccess()` for both protocols

### Phase 2: Migrate Endpoints
- [ ] Update `HandleChatCompletions` to use OpenAI adapter
- [ ] Update `HandleAnthropicMessages` to use Anthropic adapter
- [ ] Both call same unified handler internally

### Phase 3: Cleanup
- [ ] Remove duplicated code from `handler_functions.go`
- [ ] Remove duplicated code from `handler_anthropic.go`
- [ ] Consolidate `requestContext` and `anthropicRequestContext`

### Phase 4: Testing
- [ ] Add integration tests for unified handler
- [ ] Verify both endpoints show messages in frontend
- [ ] Performance benchmarks

---

## Key Files

| File | Status | Purpose |
|------|--------|---------|
| `adapter.go` | ✅ Done | Interface definitions |
| `adapter_openai.go` | ✅ Done | OpenAI passthrough |
| `adapter_anthropic.go` | ✅ Done | Anthropic translation |
| `handler_unified.go` | 📋 TODO | Single handler flow |
| `handler_functions.go` | 🔧 Refactor | Remove duplication |
| `handler_anthropic.go` | 🔧 Refactor | Remove duplication |

---

## Benefits

1. **Bug prevention** - One code path = consistent behavior
2. **Easier maintenance** - Change logic in one place
3. **Extensibility** - Add new protocols by implementing ProtocolAdapter
4. **Testability** - Test adapter translations independently
