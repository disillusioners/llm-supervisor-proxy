# Plan Review: Fix Streaming Tool Call Repair

## Executive Summary

The plan correctly identifies the core problem: **per-chunk repair is fundamentally broken** because tool call arguments are incrementally streamed across multiple chunks. The proposed solution (accumulate → repair → rewrite) is architecturally sound.

However, after reviewing the codebase, I've identified several areas where the plan can be improved for better integration with existing code and simpler implementation.

---

## Current Implementation Analysis

### What Already Exists

| Component | Location | Status |
|-----------|----------|--------|
| Tool call accumulation | [`handler_helpers.go:317-362`](pkg/proxy/handler_helpers.go:317) | ✅ Already accumulates via `toolCallArgBuilders` |
| Stream buffering | [`stream_buffer.go`](pkg/proxy/stream_buffer.go) | ✅ Stores all chunks |
| Non-streaming repair | [`race_executor.go:608`](pkg/proxy/race_executor.go:608) | ✅ Works correctly |
| Per-chunk repair (broken) | [`race_executor.go:694`](pkg/proxy/race_executor.go:694) | ❌ Broken for streaming |
| Normalizer repair (broken) | [`normalizers/tool_call_repair.go`](pkg/proxy/normalizers/tool_call_repair.go) | ❌ Same issue |

### Key Insight: Existing Accumulation Pattern

The codebase already has a working accumulation pattern in [`extractStreamChunkContent()`](pkg/proxy/handler_helpers.go:286):

```go
// toolCallArgBuilders tracks the arguments string per index (avoids += memory trap)
toolCallArgBuilders []*strings.Builder

// In extraction:
if args, ok := fn["arguments"].(string); ok {
    (*toolCallArgBuilders)[idx].WriteString(args)
}
```

**Recommendation:** Leverage this existing pattern rather than creating a new `ToolCallAccumulator` struct.

---

## Issues with Current Plan

### Issue 1: Redundant Accumulator Design

The plan proposes a new `ToolCallAccumulator` struct, but:
- `handler_helpers.go` already has `toolCallArgBuilders` that does this
- The existing pattern is well-tested and memory-efficient
- Adding a second accumulator would create confusion and duplication

### Issue 2: Unclear Integration Point

The plan doesn't clearly specify WHERE post-stream repair should happen:
- `handleStreamingResponse()` doesn't have access to start time or deadline
- The coordinator manages deadlines, not the executor
- Need to pass these parameters through the call chain

### Issue 3: Buffer Rewriting Complexity

The plan proposes `rewriteBufferWithRepairedArgs()` but:
- `streamBuffer` is append-only by design
- Rewriting requires either creating a new buffer or modifying in-place
- Both approaches have tradeoffs not fully explored

### Issue 4: Missing Internal Stream Handling

The plan mentions [`handleInternalStream()`](pkg/proxy/race_executor.go:211) needs the same treatment, but:
- Internal streaming has its own chunk construction logic
- Tool repair is already called per-chunk at line 340
- Needs same post-stream accumulation approach

---

## Improved Implementation Plan

### Phase 1: Create Lightweight Accumulator Wrapper

**Goal:** Wrap existing accumulation pattern for use in race executor.

**File:** `pkg/proxy/tool_call_accumulator.go` (new)

```go
// ToolCallAccumulator wraps the existing accumulation pattern
// for use in race_executor.go. It reuses the memory-efficient
// strings.Builder pattern from handler_helpers.go
type ToolCallAccumulator struct {
    mu sync.Mutex
    // args[index] = accumulated arguments string builder
    args map[int]*strings.Builder
    // Track tool call metadata per index
    metadata map[int]ToolCallMeta
}

type ToolCallMeta struct {
    ID   string
    Type string
    Name string
}

// ProcessChunk extracts and accumulates tool calls from a streaming chunk
// This is a side-effect - the chunk is passed through unchanged
func (a *ToolCallAccumulator) ProcessChunk(line []byte) error {
    // Parse line as SSE data
    // Extract tool_calls from delta
    // For each tool_call:
    //   - Get index
    //   - Accumulate arguments to builder
    //   - Store metadata (id, type, name) if present
}

// GetAccumulatedArgs returns all accumulated arguments
// Returns map[index]completeArgsString
func (a *ToolCallAccumulator) GetAccumulatedArgs() map[int]string {
    a.mu.Lock()
    defer a.mu.Unlock()
    
    result := make(map[int]string)
    for idx, builder := range a.args {
        result[idx] = builder.String()
    }
    return result
}

// HasToolCalls returns true if any tool calls were accumulated
func (a *ToolCallAccumulator) HasToolCalls() bool {
    return len(a.args) > 0
}
```

**Why this is better:**
- Lightweight wrapper, not a complete reimplementation
- Thread-safe for potential parallel access
- Returns simple map for easy repair processing

### Phase 2: Modify Streaming Handlers

**File:** `pkg/proxy/race_executor.go`

#### 2.1 Modify `handleStreamingResponse()`

```go
func handleStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, provider string) error {
    // ... existing setup ...
    
    // NEW: Create accumulator for this stream
    accumulator := NewToolCallAccumulator()
    streamStartTime := time.Now()
    
    for {
        // ... existing read logic ...
        
        if len(line) > 0 {
            // NEW: Accumulate tool calls BEFORE normalization
            accumulator.ProcessChunk(line)
            
            // ... existing normalization and buffering ...
            
            // REMOVE: Per-chunk repair (lines 884-904)
            // if cfg.ToolRepair.Enabled {
            //     repairedLine, repaired := repairToolCallArgumentsInChunk(normalizedLine, cfg.ToolRepair)
            //     ...
            // }
        }
        
        // ... rest of loop ...
    }
    
    // NEW: Post-stream repair (only if before deadline)
    if sawDone && accumulator.HasToolCalls() {
        if !isPastDeadline(streamStartTime, cfg.StreamDeadline) {
            repairedArgs := repairAccumulatedArgs(accumulator.GetAccumulatedArgs(), cfg.ToolRepair)
            if len(repairedArgs) > 0 {
                req.buffer = rewriteBufferWithRepairedArgs(req.buffer, repairedArgs)
            }
        } else {
            log.Printf("[TOOL-REPAIR] Stream completed after deadline, skipping repair for latency")
        }
    }
    
    return nil
}

func isPastDeadline(startTime time.Time, deadline Duration) bool {
    return time.Since(startTime) > time.Duration(deadline)
}
```

#### 2.2 Modify `handleInternalStream()`

Same pattern as above:
- Add accumulator at start
- Call `accumulator.ProcessChunk()` for each chunk
- Remove per-chunk repair at lines 339-345
- Add post-stream repair after "done" event

### Phase 3: Implement Buffer Rewriting

**File:** `pkg/proxy/buffer_rewriter.go` (new)

```go
// rewriteBufferWithRepairedArgs creates a new buffer with repaired tool call arguments
// This is a memory-efficient implementation that only rewrites chunks containing tool_calls
func rewriteBufferWithRepairedArgs(oldBuffer *streamBuffer, repairedArgs map[int]string) *streamBuffer {
    // Get all chunks from old buffer
    chunks, _ := oldBuffer.GetChunksFrom(0)
    
    // Create new buffer
    newBuffer := newStreamBuffer(oldBuffer.maxBytes)
    
    for _, chunk := range chunks {
        // Check if chunk contains tool_calls
        if hasToolCalls(chunk) {
            // Parse, repair, re-serialize
            repairedChunk := repairChunkArgs(chunk, repairedArgs)
            newBuffer.Add(repairedChunk)
        } else {
            // Pass through unchanged
            newBuffer.Add(bytes.TrimSuffix(chunk, []byte("\n")))
        }
    }
    
    return newBuffer
}

// hasToolCalls checks if a chunk contains tool_calls in delta
func hasToolCalls(chunk []byte) bool {
    // Quick string check before JSON parsing
    return bytes.Contains(chunk, []byte("tool_calls"))
}

// repairChunkArgs repairs tool call arguments in a single chunk
func repairChunkArgs(chunk []byte, repairedArgs map[int]string) []byte {
    // Strip newline if present
    chunk = bytes.TrimSuffix(chunk, []byte("\n"))
    
    // Strip "data: " prefix if present
    var prefix []byte
    if bytes.HasPrefix(chunk, []byte("data: ")) {
        prefix = []byte("data: ")
        chunk = bytes.TrimPrefix(chunk, prefix)
    }
    
    // Parse JSON
    var obj map[string]interface{}
    if err := json.Unmarshal(chunk, &obj); err != nil {
        return chunk // Can't parse, return as-is
    }
    
    // Navigate to choices[0].delta.tool_calls
    choices, ok := obj["choices"].([]interface{})
    if !ok || len(choices) == 0 {
        return chunk
    }
    
    choice, ok := choices[0].(map[string]interface{})
    if !ok {
        return chunk
    }
    
    delta, ok := choice["delta"].(map[string]interface{})
    if !ok {
        return chunk
    }
    
    toolCalls, ok := delta["tool_calls"].([]interface{})
    if !ok {
        return chunk
    }
    
    modified := false
    for _, tc := range toolCalls {
        tcMap, ok := tc.(map[string]interface{})
        if !ok {
            continue
        }
        
        // Get index
        index, ok := tcMap["index"].(float64)
        if !ok {
            continue
        }
        idx := int(index)
        
        // Check if we have repaired args for this index
        if repaired, has := repairedArgs[idx]; has {
            if fn, ok := tcMap["function"].(map[string]interface{}); ok {
                if origArgs, _ := fn["arguments"].(string); origArgs != repaired {
                    fn["arguments"] = repaired
                    modified = true
                }
            }
        }
    }
    
    if !modified {
        return chunk
    }
    
    // Re-serialize
    newChunk, err := json.Marshal(obj)
    if err != nil {
        return chunk
    }
    
    // Re-add prefix
    if len(prefix) > 0 {
        newChunk = append(prefix, newChunk...)
    }
    
    return newChunk
}
```

**Key Design Decisions:**
1. **New buffer instead of in-place:** Safer, allows graceful fallback
2. **Quick string check:** Avoid JSON parsing for non-tool-call chunks
3. **Preserve format:** Maintain "data: " prefix and newlines

### Phase 4: Implement Accumulated Args Repair

**File:** `pkg/proxy/race_executor.go` (add function)

```go
// repairAccumulatedArgs repairs accumulated tool call arguments
// Returns map[index]repairedArgs (only includes indices that were repaired)
func repairAccumulatedArgs(accumulated map[int]string, config toolrepair.Config) map[int]string {
    if !config.Enabled {
        return nil
    }
    
    repairer := toolrepair.NewRepairer(&config)
    repaired := make(map[int]string)
    
    for idx, args := range accumulated {
        // Check if already valid JSON
        var js interface{}
        if json.Unmarshal([]byte(args), &js) == nil {
            continue // Already valid, no repair needed
        }
        
        // Attempt repair
        result := repairer.RepairArguments(args, "")
        if result.Success && result.Repaired != args {
            repaired[idx] = result.Repaired
            log.Printf("[TOOL-REPAIR] Repaired tool_call[%d] arguments: %d -> %d bytes",
                idx, len(args), len(result.Repaired))
        } else if !result.Success {
            log.Printf("[WARN] Tool repair failed for tool_call[%d], using original args", idx)
        }
    }
    
    return repaired
}
```

### Phase 5: Remove Per-Chunk Repair Code

**Files to modify:**

1. **`pkg/proxy/race_executor.go`:**
   - Remove `repairToolCallArgumentsInChunk()` function (lines 692-792)
   - Remove calls to it in `handleStreamingResponse()` (lines 884-904)
   - Remove calls to it in `handleInternalStream()` (lines 339-345)

2. **`pkg/proxy/normalizers/tool_call_repair.go`:**
   - Mark as **DEPRECATED** with clear comment
   - Keep file but add warning that it's broken for streaming
   - Consider adding compile-time warning or metric

---

## Edge Cases Handling

| Case | Handling | Location |
|------|----------|----------|
| Multiple tool calls | Accumulate by index, repair each | `ToolCallAccumulator` |
| Tool call with no args | Skip repair | `repairAccumulatedArgs` |
| Repair fails | Keep original, log warning | `repairAccumulatedArgs` |
| Stream error mid-way | Don't repair, send what we have | `handleStreamingResponse` |
| Large tool call args | Already handled by buffer limits | `streamBuffer.maxBytes` |
| **Past stream deadline** | **Skip repair entirely** | `isPastDeadline` check |
| Buffer rewrite fails | Fall back to original buffer | `rewriteBufferWithRepairedArgs` |
| Invalid JSON in chunk | Pass through unchanged | `repairChunkArgs` |

---

## Testing Strategy

### Unit Tests

| Test | Description |
|------|-------------|
| `TestToolCallAccumulator_Single` | Single tool call across 4 chunks |
| `TestToolCallAccumulator_Multiple` | Multiple tool calls with different indices |
| `TestToolCallAccumulator_Empty` | No tool calls in stream |
| `TestRepairAccumulatedArgs_Valid` | Already valid JSON - no repair |
| `TestRepairAccumulatedArgs_Malformed` | Malformed JSON - repair succeeds |
| `TestRepairAccumulatedArgs_Unrepairable` | Can't repair - keep original |
| `TestRewriteBuffer_PreservesFormat` | Output format matches input |
| `TestRewriteBuffer_OnlyToolCallChunks` | Non-tool chunks unchanged |
| `TestIsPastDeadline` | Deadline boundary conditions |

### Integration Tests

| Test | Description |
|------|-------------|
| `TestStreamingRepair_EndToEnd` | Full stream with malformed args |
| `TestStreamingRepair_PastDeadline` | Verify repair skipped after deadline |
| `TestStreamingRepair_MultipleToolCalls` | All tool calls repaired correctly |
| `TestInternalStreamRepair` | Same for internal provider path |

---

## Migration Path

1. **Phase 1:** Add accumulator (no behavior change, just accumulation)
2. **Phase 2:** Add post-stream repair behind feature flag
3. **Phase 3:** Enable by default, monitor metrics
4. **Phase 4:** Remove per-chunk repair code

### Feature Flag

Add to config:
```go
type ConfigSnapshot struct {
    // ...
    ToolRepair ToolRepairConfig `json:"tool_repair"`
}

type ToolRepairConfig struct {
    Enabled           bool   `json:"enabled"`
    StreamingRepairV2 bool   `json:"streaming_repair_v2"` // New feature flag
    // ... existing fields ...
}
```

---

## Performance Considerations

### Memory Impact

| Operation | Impact | Mitigation |
|-----------|--------|------------|
| Accumulation | +1 copy of args strings | Use strings.Builder (already efficient) |
| Buffer rewrite | +1 temporary buffer | Only when repair needed |
| JSON parsing | CPU overhead | Quick string check first |

### Latency Impact

| Scenario | Impact |
|----------|--------|
| No tool calls | None (early exit) |
| Valid tool calls | Minimal (validation only) |
| Malformed tool calls | +repair time (before deadline only) |
| Past deadline | None (repair skipped) |

---

## Summary of Changes

| File | Action | Lines Changed |
|------|--------|---------------|
| `pkg/proxy/tool_call_accumulator.go` | **NEW** | ~100 |
| `pkg/proxy/buffer_rewriter.go` | **NEW** | ~120 |
| `pkg/proxy/race_executor.go` | **MODIFY** | ~50 added, ~120 removed |
| `pkg/proxy/normalizers/tool_call_repair.go` | **DEPRECATE** | ~5 (comments) |
| `pkg/proxy/tool_call_accumulator_test.go` | **NEW** | ~200 |
| `pkg/proxy/buffer_rewriter_test.go` | **NEW** | ~150 |

**Total:** ~400 lines added, ~120 lines removed, net ~280 lines

---

## Open Questions Resolved

1. **Performance impact:** Acceptable. Only rewrite when tool calls detected, quick string check before JSON parsing.

2. **Error handling:** Send original buffer on rewrite failure (graceful degradation).

3. **Fixer model integration:** Yes, works on accumulated args. Already supported by `toolrepair.Repairer`.

---

## Comparison: Original Plan vs Improved Plan

| Aspect | Original Plan | Improved Plan |
|--------|---------------|---------------|
| Accumulator | New struct from scratch | Lightweight wrapper, reuses patterns |
| Integration point | Unclear | Explicit function modification |
| Buffer rewrite | Single function | Modular with helper functions |
| Error handling | Not specified | Graceful degradation at each step |
| Feature flag | Not mentioned | `StreamingRepairV2` flag |
| Testing | Basic list | Comprehensive unit + integration |
| Migration | 4 phases | 4 phases with feature flag |

---

## Recommendation

**Proceed with the improved plan.** It:
1. Leverages existing code patterns
2. Has clearer integration points
3. Includes comprehensive error handling
4. Provides safe migration path with feature flag
5. Has detailed testing strategy
