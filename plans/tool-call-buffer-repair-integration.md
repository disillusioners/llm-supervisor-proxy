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

### Phase 2: Update race_executor.go

#### 2.1 Remove ToolCallAccumulator Usage

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

### Option A: Feature Flag (Recommended)

Add `TOOL_CALL_REPAIR_STREAMING=true` (default: false for now)

1. If true: Use new ToolCallBuffer with repair
2. If false: Use old ToolCallAccumulator + post-stream repair

This allows gradual rollout and A/B testing.

### Option B: Direct Replacement

Remove old code immediately and replace with new implementation.

**Risk:** If issues arise, no fallback.

## Impact Analysis

### Components Affected

| Component | Change |
|-----------|--------|
| `tool_call_buffer.go` | Add repair integration |
| `race_executor.go` | Remove accumulator, simplify flow |
| `tool_call_accumulator.go` | DELETE |
| `buffer_rewriter.go` | Evaluate for removal |
| Tests | Update for new behavior |

### Behavior Changes

| Scenario | Before | After |
|----------|--------|-------|
| Malformed JSON in tool call | Client receives malformed → post-stream repair | Client receives repaired immediately |
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

## Estimated Changes

| File | Type | Lines Changed |
|------|------|---------------|
| `pkg/proxy/tool_call_buffer.go` | Modify | ~50 |
| `pkg/proxy/race_executor.go` | Modify | ~100 (net reduction) |
| `pkg/proxy/tool_call_accumulator.go` | DELETE | -258 |
| `pkg/proxy/tool_call_accumulator_test.go` | DELETE | -500 |
| `pkg/proxy/buffer_rewriter.go` | Evaluate | TBD |
| `pkg/proxy/tool_call_buffer_test.go` | Modify | ~100 |

**Net result:** ~500 fewer lines of code, simpler architecture

## Conclusion

This refactoring simplifies the tool call processing pipeline by:
1. Eliminating redundant accumulation
2. Providing real-time repair to clients
3. Reducing memory usage
4. Removing post-stream buffer rewriting

The main risk is LLM-based repair latency during streaming, which can be mitigated by using only library-based repair for streaming scenarios.
