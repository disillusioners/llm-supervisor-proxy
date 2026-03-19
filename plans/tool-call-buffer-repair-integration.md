# Tool Call Buffer + Repair Integration Plan

## Status: PROPOSED

## Summary

Integrate ToolRepair into ToolCallBuffer to emit repaired tool calls immediately when complete, eliminating the need for ToolCallAccumulator and post-stream buffer rewriting.

## Current Architecture (Complex)

```
Streaming Phase:
┌─────────────────────────────────────────────────────────────────────────────┐
│  Upstream → Normalizer → ToolCallBuffer → Stream Buffer → Client           │
│                           ↓ (passive)                                       │
│                    ToolCallAccumulator                                       │
└─────────────────────────────────────────────────────────────────────────────┘
                                    ↓
Post-Stream Phase:
┌─────────────────────────────────────────────────────────────────────────────┐
│  ToolCallAccumulator → ToolRepair → Buffer Rewriter → Repaired Buffer      │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Problems:**
1. Two components doing similar accumulation (ToolCallBuffer + ToolCallAccumulator)
2. Post-stream repair requires buffer rewrite (memory intensive)
3. Client receives potentially malformed data before repair
4. Complex flow with multiple stages

## Proposed Architecture (Simplified)

```
Streaming Phase:
┌─────────────────────────────────────────────────────────────────────────────┐
│  Upstream → Normalizer → ToolCallBuffer → Stream Buffer → Client           │
│                           ↓                                                 │
│                    (accumulate fragments)                                   │
│                           ↓                                                 │
│                    (valid JSON?)                                            │
│                           ↓                                                 │
│                    ToolRepair (if needed)                                   │
│                           ↓                                                 │
│                    Emit repaired chunk                                      │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Benefits:**
1. Single component handles accumulation + repair
2. Client receives repaired data immediately
3. No post-stream buffer rewriting needed
4. Simpler code, lower memory usage
5. Lower latency (repair happens during streaming)

## Implementation Plan

### Phase 0: Move ToolCallBuffer to Shared Package (REQUIRED)

To avoid circular dependencies when `ultimatemodel` package needs to use `ToolCallBuffer`:

**Create:** `pkg/toolcall/buffer.go`

```go
package toolcall

import (
    "sync"
    "github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// ToolCallBuffer accumulates tool call fragments and emits complete tool calls
// with optional JSON repair when complete.
type ToolCallBuffer struct {
    mu              sync.Mutex
    builders        map[int]*ToolCallBuilder
    totalSize       int64
    maxSize         int64
    modelID         string
    requestID       string
    
    // Tool repair integration
    repairConfig    *toolrepair.Config
    repairer        *toolrepair.Repairer
    repairStats     RepairStats
}

// ... rest of the implementation ...
```

**Files to create:**
- `pkg/toolcall/buffer.go` - Move from `pkg/proxy/tool_call_buffer.go`
- `pkg/toolcall/buffer_test.go` - Move from `pkg/proxy/tool_call_buffer_test.go`

**Files to update imports:**
- `pkg/proxy/race_executor.go`
- `pkg/proxy/internal_handler.go`
- `pkg/ultimatemodel/handler_internal.go`

### Phase 1: Enhance ToolCallBuffer

#### 1.1 Add ToolRepair Integration

**File:** `pkg/proxy/tool_call_buffer.go`

```go
type ToolCallBuffer struct {
    mu              sync.Mutex
    builders        map[int]*ToolCallBuilder
    totalSize       int64
    maxSize         int64
    modelID         string
    requestID       string
    
    // NEW: Tool repair integration
    repairConfig    *toolrepair.Config
    repairer        *toolrepair.Repairer
    repairStats     RepairStats  // Track repairs for logging/events
}

type RepairStats struct {
    Attempted    int
    Successful   int
    Failed       int
}
```

#### 1.2 Modify emitToolCall to Repair Before Emitting

```go
func (b *ToolCallBuffer) emitToolCall(idx int) []byte {
    builder := b.builders[idx]
    args := builder.Arguments.String()
    
    // NEW: Repair arguments if needed
    if b.repairer != nil && args != "" {
        // Check if already valid JSON
        var js interface{}
        if json.Unmarshal([]byte(args), &js) != nil {
            // Not valid JSON - attempt repair
            b.repairStats.Attempted++
            result := b.repairer.RepairArguments(args, builder.Name)
            if result.Success {
                args = result.Repaired
                b.repairStats.Successful++
                log.Printf("[TOOL-BUFFER] Repaired tool_call[%d] arguments during streaming", idx)
            } else {
                b.repairStats.Failed++
                log.Printf("[WARN] Tool repair failed for tool_call[%d], emitting original", idx)
            }
        }
    }
    
    // Build chunk with (potentially repaired) arguments
    chunk := map[string]interface{}{
        // ... same as before ...
        "function": map[string]interface{}{
            "name":      builder.Name,
            "arguments": args,  // Use repaired args
        },
    }
    // ...
}
```

#### 1.3 Add Constructor with Repair Config

```go
func NewToolCallBufferWithRepair(maxSize int64, modelID, requestID string, repairConfig *toolrepair.Config) *ToolCallBuffer {
    b := NewToolCallBuffer(maxSize, modelID, requestID)
    if repairConfig != nil && repairConfig.Enabled {
        b.repairConfig = repairConfig
        b.repairer = toolrepair.NewRepairer(repairConfig)
    }
    return b
}
```

### Phase 2: Update race_executor.go (External Path)

#### 2.1 Remove ToolCallAccumulator Usage in handleStreamingResponse()

**File:** `pkg/proxy/race_executor.go` - `handleStreamingResponse()` function

**Before:**
```go
// Create accumulator for tool call arguments
accumulator := NewToolCallAccumulator()

// ... in loop ...
if err := accumulator.ProcessChunk([]byte(lineToProcess)); err != nil {
    log.Printf("[DEBUG] failed to accumulate chunk: %v", err)
}

// ... post-stream ...
if accumulator.HasToolCalls() && cfg.ToolRepair.Enabled {
    repairedArgs := repairAccumulatedArgs(accumulator.GetAccumulatedArgs(), cfg.ToolRepair)
    if len(repairedArgs) > 0 {
        req.buffer = rewriteBufferWithRepairedArgs(req.buffer, repairedArgs)
    }
}
```

**After:**
```go
// Create tool call buffer with repair integration
var toolCallBuffer *ToolCallBuffer
if !cfg.ToolCallBufferDisabled {
    toolCallBuffer = NewToolCallBufferWithRepair(
        cfg.ToolCallBufferMaxSize,
        req.modelID,
        fmt.Sprintf("%d", req.id),
        &cfg.ToolRepair,  // Pass repair config
    )
}

// ... in loop ...
// No separate accumulation needed - buffer handles it

// ... post-stream ...
// No repair needed - already done during streaming
// Only flush remaining buffered tool calls
if toolCallBuffer != nil {
    flushChunks := toolCallBuffer.Flush()
    // ... add to buffer ...
    
    // Log repair stats if any repairs occurred
    stats := toolCallBuffer.GetRepairStats()
    if stats.Attempted > 0 {
        log.Printf("[TOOL-BUFFER] Repair stats: attempted=%d, success=%d, failed=%d",
            stats.Attempted, stats.Successful, stats.Failed)
    }
}
```

### Phase 2b: Update race_executor.go (Internal Path)

#### 2b.1 Add ToolCallBuffer to handleInternalStream()

**File:** `pkg/proxy/race_executor.go` - `handleInternalStream()` function (lines 203-460)

**Current State:**
- Uses `ToolCallAccumulator` for accumulation (line 220)
- Has post-stream repair logic (lines 424-444)
- Does NOT use `ToolCallBuffer` (explicitly noted in comment at lines 223-226)

**Why it was excluded:**
```go
// NOTE: Tool call buffering is NOT applied to internal streams because:
// 1. Internal providers (Anthropic, etc.) generate well-formed streaming output
// 2. The tool call buffer is specifically for weak external upstream clients
// 3. Internal path converts provider-specific format to OpenAI format directly
```

**However**, tool call arguments can still be malformed even from internal providers (e.g., Anthropic can generate invalid JSON). The repair logic should be integrated.

**Proposed Change:**

```go
func handleInternalStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string, normCtx *normalizers.NormalizeContext, toolRepairConfig toolrepair.Config, streamDeadline time.Duration) error {
    eventCh, err := provider.StreamChatCompletion(ctx, req)
    if err != nil {
        return err
    }

    upstreamReq.MarkStreaming()

    // Track state for proper streaming format
    firstChunk := true
    nextToolCallIndex := 0
    seenToolCallIDs := make(map[string]int)

    // NEW: Create tool call buffer with repair integration
    // Even internal providers can generate malformed JSON in tool call arguments
    var toolCallBuffer *ToolCallBuffer
    toolCallBufferMaxSize := int64(5 * 1024 * 1024) // 5MB default
    toolCallBuffer = NewToolCallBufferWithRepair(
        toolCallBufferMaxSize,
        internalModel,
        fmt.Sprintf("%d", upstreamReq.id),
        &toolRepairConfig,
    )
    streamStartTime := time.Now()

    for event := range eventCh {
        // ... existing event handling ...

        case "tool_call":
            if len(event.ToolCalls) > 0 {
                // ... build toolCalls chunk ...
                
                // NEW: Process through tool call buffer with repair
                // The buffer will accumulate fragments and repair when complete
                chunksToEmit := toolCallBuffer.ProcessChunk([]byte(line))
                for _, chunk := range chunksToEmit {
                    if !upstreamReq.buffer.Add(chunk) {
                        return fmt.Errorf("buffer limit exceeded")
                    }
                }
            }

        case "done":
            // ... write final chunk and [DONE] ...

            // NEW: Flush any remaining buffered tool calls
            flushChunks := toolCallBuffer.Flush()
            for _, chunk := range flushChunks {
                if !upstreamReq.buffer.Add(chunk) {
                    log.Printf("[WARN] Race attempt %d (internal): failed to flush tool call chunk", upstreamReq.id)
                }
            }

            // Log repair stats
            stats := toolCallBuffer.GetRepairStats()
            if stats.Attempted > 0 {
                log.Printf("[TOOL-BUFFER] Race attempt %d (internal): Repair stats: attempted=%d, success=%d, failed=%d",
                    upstreamReq.id, stats.Attempted, stats.Successful, stats.Failed)
            }

            return nil
        }
    }
    // ...
}
```

**Key Changes:**
1. Replace `ToolCallAccumulator` with `ToolCallBufferWithRepair`
2. Process tool_call events through the buffer
3. Flush remaining chunks at "done" event
4. Remove post-stream `rewriteBufferWithRepairedArgs()` call (no longer needed)

### Phase 2c: Update internal_handler.go

#### 2c.1 Add ToolCallBuffer to InternalHandler

**File:** `pkg/proxy/internal_handler.go`

**Current State:**
- Has `SetRepairer()` method (lines 40-44)
- Repairer is passed to `OpenAIProvider` (lines 72-77)
- NO `ToolCallBuffer` or `ToolCallAccumulator` in streaming path
- Tool call chunks are written directly to client without buffering

**Problem:**
- If internal provider returns malformed tool call JSON, it goes directly to client
- The `SetRepairer()` is only used by the provider internally, not for stream repair

**Proposed Change:**

```go
// Add to InternalHandler struct
type InternalHandler struct {
    config        *models.ModelConfig
    resolver      models.ModelsConfigInterface
    bufferStore   *bufferstore.BufferStore
    requestID     string
    repairer      *toolrepair.Repairer
    eventCallback toolrepair.RepairEventCallback
    
    // NEW: Tool call buffer config
    toolCallBufferMaxSize int64
    toolRepairConfig      *toolrepair.Config
}

// Add setter for tool call buffer config
func (h *InternalHandler) SetToolCallBufferConfig(maxSize int64, repairConfig *toolrepair.Config) {
    h.toolCallBufferMaxSize = maxSize
    h.toolRepairConfig = repairConfig
}

// Update handleStream() to use ToolCallBuffer
func (h *InternalHandler) handleStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, w http.ResponseWriter, internalModel string) error {
    eventCh, err := provider.StreamChatCompletion(ctx, req)
    if err != nil {
        return err
    }

    // ... SSE headers ...

    // NEW: Create tool call buffer with repair
    var toolCallBuffer *ToolCallBuffer
    if h.toolCallBufferMaxSize > 0 {
        toolCallBuffer = NewToolCallBufferWithRepair(
            h.toolCallBufferMaxSize,
            internalModel,
            h.requestID,
            h.toolRepairConfig,
        )
    }

    for event := range eventCh {
        switch event.Type {
        // ... content case ...

        case "tool_call":
            if len(event.ToolCalls) > 0 {
                // ... build chunk ...
                
                // NEW: Process through tool call buffer
                if toolCallBuffer != nil {
                    chunksToEmit := toolCallBuffer.ProcessChunk(data)
                    for _, chunk := range chunksToEmit {
                        fmt.Fprintf(w, "data: %s\n\n", chunk)
                        flusher.Flush()
                    }
                } else {
                    // Fallback: emit directly
                    fmt.Fprintf(w, "data: %s\n\n", data)
                    flusher.Flush()
                }
            }

        case "done":
            // NEW: Flush remaining buffered tool calls
            if toolCallBuffer != nil {
                flushChunks := toolCallBuffer.Flush()
                for _, chunk := range flushChunks {
                    fmt.Fprintf(w, "data: %s\n\n", chunk)
                    flusher.Flush()
                }
            }
            
            // ... write final chunk and [DONE] ...
            return nil
        }
    }
    // ...
}
```

### Phase 2d: Update ultimatemodel/handler_internal.go

#### 2d.1 Add ToolCallBuffer to UltimateModel Internal Path

**File:** `pkg/ultimatemodel/handler_internal.go`

**Current State:**
- NO `ToolCallAccumulator`
- NO `ToolCallBuffer`
- NO repair logic
- Tool call chunks written directly to client

**Problem:**
- UltimateModel internal path has no tool call repair at all
- Malformed JSON goes directly to client

**Proposed Change:**

```go
// Add to Handler struct or pass via parameters
type Handler struct {
    // ... existing fields ...
    
    // NEW: Tool call buffer config
    toolCallBufferMaxSize int64
    toolRepairConfig      *toolrepair.Config
}

// Update handleInternalStream()
func (h *Handler) handleInternalStream(
    ctx context.Context,
    provider providers.Provider,
    req *providers.ChatCompletionRequest,
    w http.ResponseWriter,
    internalModel string,
) error {
    eventCh, err := provider.StreamChatCompletion(ctx, req)
    if err != nil {
        return err
    }

    // ... SSE headers ...

    // NEW: Create tool call buffer with repair
    var toolCallBuffer *proxy.ToolCallBuffer
    if h.toolCallBufferMaxSize > 0 && h.toolRepairConfig != nil {
        toolCallBuffer = proxy.NewToolCallBufferWithRepair(
            h.toolCallBufferMaxSize,
            internalModel,
            "", // request ID not available here
            h.toolRepairConfig,
        )
    }

    for event := range eventCh {
        switch event.Type {
        // ... content and thinking cases ...

        case "tool_call":
            if len(event.ToolCalls) > 0 {
                // ... build chunk ...
                
                // NEW: Process through tool call buffer
                if toolCallBuffer != nil {
                    chunksToEmit := toolCallBuffer.ProcessChunk(data)
                    for _, chunk := range chunksToEmit {
                        fmt.Fprintf(w, "data: %s\n\n", chunk)
                        flusher.Flush()
                    }
                } else {
                    fmt.Fprintf(w, "data: %s\n\n", data)
                    flusher.Flush()
                }
            }

        case "done":
            // NEW: Flush remaining buffered tool calls
            if toolCallBuffer != nil {
                flushChunks := toolCallBuffer.Flush()
                for _, chunk := range flushChunks {
                    fmt.Fprintf(w, "data: %s\n\n", chunk)
                    flusher.Flush()
                }
            }
            
            // ... write final chunk and [DONE] ...
            return nil
        }
    }
    // ...
}
```

**Note:** This requires importing `proxy.ToolCallBuffer` which may create a circular dependency. Consider:
1. Moving `ToolCallBuffer` to a shared package (e.g., `pkg/toolcall/buffer.go`)
2. Or duplicating the buffer logic in `ultimatemodel` package

### Phase 3: Cleanup

#### 3.1 Remove ToolCallAccumulator

Delete files:
- `pkg/proxy/tool_call_accumulator.go`
- `pkg/proxy/tool_call_accumulator_test.go`

#### 3.2 Remove Buffer Rewriter (if only used for tool repair)

Evaluate if `buffer_rewriter.go` is still needed for other purposes. If not, remove:
- `pkg/proxy/buffer_rewriter.go`
- `pkg/proxy/buffer_rewriter_test.go`

#### 3.3 Remove Deprecated Functions

In `race_executor.go`:
- Remove `repairAccumulatedArgs()`
- Remove `repairToolCallArgumentsInChunk()` (already deprecated)
- Keep `repairToolCallArgumentsInNonStreamingResponse()` (still needed for non-streaming)

### Phase 4: Update Tests

#### 4.1 Update ToolCallBuffer Tests

Add tests for repair integration:
```go
func TestToolCallBuffer_RepairOnEmit(t *testing.T) {
    config := &toolrepair.Config{
        Enabled:    true,
        MaxSize:    1024 * 1024,
        Strategy:   "library_repair",
    }
    buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", config)
    
    // Buffer malformed JSON
    chunks := buffer.ProcessChunk([]byte(
        `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{key: value}"}}]}}]}`
    ))
    
    // Should not emit (incomplete)
    if len(chunks) != 0 {
        t.Errorf("Should buffer incomplete tool call")
    }
    
    // Add closing brace to complete
    chunks = buffer.ProcessChunk([]byte(
        `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`
    ))
    
    // Should emit repaired JSON
    if len(chunks) != 1 {
        t.Fatalf("Expected 1 chunk, got %d", len(chunks))
    }
    
    // Verify arguments are repaired
    var obj map[string]interface{}
    // ... parse and verify ...
}
```

## Migration Strategy

### Direct Replacement (Selected Approach)

Remove old code immediately and replace with new implementation across all streaming paths.

**Implementation Order:**

1. **Phase 1:** Enhance `ToolCallBuffer` with repair integration
   - Add `NewToolCallBufferWithRepair()` constructor
   - Add repair logic in `emitToolCall()`
   - Add `GetRepairStats()` method

2. **Phase 2a:** Update external path (`handleStreamingResponse()`)
   - Replace `ToolCallAccumulator` with `ToolCallBufferWithRepair`
   - Remove post-stream repair logic

3. **Phase 2b:** Update internal race path (`handleInternalStream()`)
   - Replace `ToolCallAccumulator` with `ToolCallBufferWithRepair`
   - Remove post-stream repair logic

4. **Phase 2c:** Update direct internal path (`internal_handler.go`)
   - Add `ToolCallBuffer` integration
   - Add config setters

5. **Phase 2d:** Update UltimateModel internal path
   - Move `ToolCallBuffer` to shared package `pkg/toolcall/` to avoid circular dependency
   - Add `ToolCallBuffer` integration

6. **Phase 3:** Cleanup
   - Delete `tool_call_accumulator.go` and tests
   - Remove deprecated functions from `race_executor.go`
   - Evaluate `buffer_rewriter.go` for removal

7. **Phase 4:** Update tests
   - Add repair integration tests
   - Update existing tests for new behavior

## Impact Analysis

### Components Affected

| Component | Change |
|-----------|--------|
| `tool_call_buffer.go` | Add repair integration |
| `race_executor.go` - `handleStreamingResponse()` | Remove accumulator, simplify flow (external path) |
| `race_executor.go` - `handleInternalStream()` | Replace accumulator with ToolCallBuffer (internal path) |
| `internal_handler.go` | Add ToolCallBuffer integration |
| `ultimatemodel/handler_internal.go` | Add ToolCallBuffer integration |
| `tool_call_accumulator.go` | DELETE |
| `buffer_rewriter.go` | Evaluate for removal |
| Tests | Update for new behavior |

### Streaming Paths Summary

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           REQUEST FLOW PATHS                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. EXTERNAL PATH (race_executor.go)                                        │
│     Client → Handler → RaceCoordinator → executeExternalRequest             │
│                    ↓                                                        │
│     handleStreamingResponse() → ToolCallBuffer → StreamBuffer → Client      │
│                                                                             │
│  2. INTERNAL PATH - Race Retry (race_executor.go)                           │
│     Client → Handler → RaceCoordinator → executeInternalRequest             │
│                    ↓                                                        │
│     handleInternalStream() → ToolCallBuffer → StreamBuffer → Client         │
│                                                                             │
│  3. INTERNAL PATH - Direct (internal_handler.go)                            │
│     Client → InternalHandler → Provider → handleStream()                    │
│                    ↓                                                        │
│     handleStream() → ToolCallBuffer → SSE Response → Client                 │
│                                                                             │
│  4. INTERNAL PATH - UltimateModel (ultimatemodel/handler_internal.go)       │
│     Client → UltimateModel → executeInternal() → handleInternalStream()     │
│                    ↓                                                        │
│     handleInternalStream() → ToolCallBuffer → SSE Response → Client         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Behavior Changes

| Scenario | Before | After |
|----------|--------|-------|
| Malformed JSON in tool call (external) | Client receives malformed → post-stream repair | Client receives repaired immediately |
| Malformed JSON in tool call (internal race) | Post-stream buffer rewrite | Client receives repaired immediately |
| Malformed JSON in tool call (internal direct) | **No repair** - client receives malformed | Client receives repaired immediately |
| Malformed JSON in tool call (ultimate model) | **No repair** - client receives malformed | Client receives repaired immediately |
| Repair failure | Original args in buffer | Original args emitted (same result) |
| Memory usage | Double accumulation (buffer + accumulator) | Single accumulation |
| Latency | Post-stream repair delay | Real-time repair during streaming |

### Loop Detection

**No impact** - Loop detection uses `extractToolCallActions()` from raw chunks in `handler_helpers.go`, which operates independently of ToolCallAccumulator.

## Open Questions

1. **Event Publishing:** Should we publish repair events during streaming or batch at end?
   - Current: Batch at end
   - Proposed: Per-repair events (more real-time but more events)

2. **Repair Latency:** LLM-based repair could add latency during streaming
   - Mitigation: Only use library_repair strategy for streaming, not llm_fallback

3. **Stats Tracking:** How to expose repair stats to frontend?
   - Option 1: Add to existing events
   - Option 2: New metric endpoint

4. **Circular Dependency (IMPORTANT):** `ultimatemodel/handler_internal.go` needs `ToolCallBuffer` from `pkg/proxy`
   - Option 1: Move `ToolCallBuffer` to a shared package (e.g., `pkg/toolcall/buffer.go`)
   - Option 2: Create an interface that both packages can use
   - Option 3: Duplicate the buffer logic in `ultimatemodel` package (not recommended)
   - **Recommendation:** Option 1 - Move to shared package to avoid code duplication

## Estimated Changes

| File | Type | Lines Changed |
|------|------|---------------|
| `pkg/proxy/tool_call_buffer.go` | Modify | ~50 |
| `pkg/proxy/race_executor.go` - `handleStreamingResponse()` | Modify | ~50 (net reduction) |
| `pkg/proxy/race_executor.go` - `handleInternalStream()` | Modify | ~50 (net reduction) |
| `pkg/proxy/internal_handler.go` | Modify | ~80 |
| `pkg/ultimatemodel/handler_internal.go` | Modify | ~80 |
| `pkg/proxy/tool_call_accumulator.go` | DELETE | -258 |
| `pkg/proxy/tool_call_accumulator_test.go` | DELETE | -500 |
| `pkg/proxy/buffer_rewriter.go` | Evaluate | TBD |
| `pkg/proxy/tool_call_buffer_test.go` | Modify | ~100 |
| `pkg/proxy/internal_handler_test.go` | Add | ~100 (new tests) |
| `pkg/ultimatemodel/handler_internal_test.go` | Add | ~100 (new tests) |

**Net result:** ~200 fewer lines of code, simpler architecture, complete coverage of all streaming paths

## Conclusion

This refactoring simplifies the tool call processing pipeline by:
1. Eliminating redundant accumulation
2. Providing real-time repair to clients
3. Reducing memory usage
4. Removing post-stream buffer rewriting

The main risk is LLM-based repair latency during streaming, which can be mitigated by using only library-based repair for streaming scenarios.
