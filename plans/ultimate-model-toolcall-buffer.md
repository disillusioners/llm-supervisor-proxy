# Ultimate Model Tool Call Buffer Support

## Problem Statement

The Ultimate Model external handler (`pkg/ultimatemodel/handler_external.go`) does not support the tool call buffer feature. When the ultimate model uses an external upstream (like LiteLLM), tool call fragments are streamed directly to clients without buffering/repair.

This causes issues for weak streaming clients that cannot handle incremental tool call fragments with partial JSON in the `arguments` field.

## Current State

| Component | Tool Call Buffer Support |
|-----------|-------------------------|
| Internal Ultimate Model (`handler_internal.go`) | ✅ Yes |
| External Ultimate Model (`handler_external.go`) | ❌ No |
| Race Executor External (`race_executor.go`) | ✅ Yes |

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    ULTIMATE MODEL REQUEST FLOW                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Execute()                                                              │
│      │                                                                  │
│      ├─► modelCfg.Internal == TRUE                                     │
│      │       │                                                          │
│      │       └─► executeInternal()                                      │
│      │               │                                                  │
│      │               └─► handleInternalStream()                         │
│      │                       │                                          │
│      │                       ├─► ToolCallBuffer ✓                      │
│      │                       │                                          │
│      │                       └─► Client                                 │
│      │                                                                  │
│      └─► modelCfg.Internal == FALSE (External Upstream)                │
│              │                                                          │
│              └─► executeExternal()                                      │
│                      │                                                  │
│                      └─► streamResponse()  <-- MISSING TOOL CALL BUFFER │
│                              │                                          │
│                              └─► Client (raw fragments!)               │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

## Solution

Add tool call buffer support to the external ultimate model handler, following the same pattern used in:
1. `pkg/proxy/race_executor.go` - `handleStreamingResponse()`
2. `pkg/ultimatemodel/handler_internal.go` - `handleInternalStream()`

## Implementation Plan

### 1. Modify `handler_external.go`

#### 1.1 Update `streamResponse()` function

**Current implementation (lines 111-140):**
```go
func (h *Handler) streamResponse(w http.ResponseWriter, resp *http.Response) error {
    // Set SSE headers
    w.Header().Set("Content-Type", "text/event-stream")
    // ...
    
    reader := bufio.NewReader(resp.Body)
    for {
        line, err := reader.ReadString('\n')
        // ...
        
        // Write line directly  <-- PROBLEM: No buffering!
        fmt.Fprint(w, line)
        flusher.Flush()
    }
}
```

**New implementation:**
```go
func (h *Handler) streamResponse(w http.ResponseWriter, resp *http.Response, modelID string) error {
    // Set SSE headers
    w.Header().Set("Content-Type", "text/event-stream")
    // ...
    
    // Create tool call buffer (same pattern as handleInternalStream)
    var toolCallBuffer *toolcall.ToolCallBuffer
    if !h.toolCallBufferDisabled && h.toolRepairConfig != nil && h.toolRepairConfig.Enabled {
        toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
            h.toolCallBufferMaxSize,
            modelID,
            "ultimate-external",
            h.toolRepairConfig,
        )
    } else if !h.toolCallBufferDisabled {
        toolCallBuffer = toolcall.NewToolCallBuffer(
            h.toolCallBufferMaxSize,
            modelID,
            "ultimate-external",
        )
    }
    
    reader := bufio.NewReader(resp.Body)
    for {
        line, err := reader.ReadString('\n')
        // ...
        
        // Process through tool call buffer
        var chunksToEmit [][]byte
        if toolCallBuffer != nil {
            chunksToEmit = toolCallBuffer.ProcessChunk([]byte(line))
        } else {
            chunksToEmit = [][]byte{[]byte(line)}
        }
        
        // Write all chunks
        for _, chunk := range chunksToEmit {
            w.Write(chunk)
        }
        flusher.Flush()
    }
    
    // Flush remaining buffered tool calls at stream end
    if toolCallBuffer != nil {
        flushChunks := toolCallBuffer.Flush()
        for _, chunk := range flushChunks {
            w.Write(chunk)
        }
        
        // Log repair stats
        stats := toolCallBuffer.GetRepairStats()
        if stats.Attempted > 0 {
            log.Printf("[TOOL-BUFFER] UltimateModel External: Repair stats: attempted=%d, success=%d, failed=%d",
                stats.Attempted, stats.Successful, stats.Failed)
        }
    }
}
```

#### 1.2 Update `executeExternal()` to pass model ID

Change the call to `streamResponse()`:
```go
// Before
return h.streamResponse(w, resp)

// After
return h.streamResponse(w, resp, modelCfg.ID)
```

### 2. Required Imports

Add to `handler_external.go`:
```go
import (
    // ... existing imports
    "log"
    
    "github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
)
```

### 3. Configuration Fields Already Exist

The `Handler` struct already has the necessary fields (defined in `handler.go`):
```go
type Handler struct {
    // ... other fields
    
    // Tool call buffer configuration
    toolCallBufferMaxSize  int64
    toolCallBufferDisabled bool
    toolRepairConfig       *toolrepair.Config
}
```

And the setter method exists:
```go
func (h *Handler) SetToolCallBufferConfig(maxSize int64, disabled bool, repairConfig *toolrepair.Config)
```

### 4. Verify Configuration is Being Set

Check that `pkg/proxy/handler.go` calls `SetToolCallBufferConfig()` when initializing the ultimate model handler.

## Files to Modify

| File | Changes |
|------|---------|
| `pkg/ultimatemodel/handler_external.go` | Add tool call buffer to `streamResponse()` |
| `pkg/proxy/handler.go` | Verify `SetToolCallBufferConfig()` is called |

## Testing

### Unit Tests

Add test cases in `pkg/ultimatemodel/handler_external_test.go`:
1. Test tool call fragments are buffered until complete
2. Test complete tool calls are emitted immediately
3. Test content chunks pass through without buffering
4. Test flush at stream end emits remaining buffered tool calls
5. Test repair is applied when tool call becomes complete

### Integration Tests

Use the existing mock LLM server to test:
1. External ultimate model receives fragmented tool calls
2. Client receives complete, valid JSON in tool call arguments

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| Buffer disabled (`toolCallBufferDisabled=true`) | Pass through directly (current behavior) |
| Buffer size exceeded | Emit what we have, reset builder, continue |
| Stream ends with incomplete tool call | Flush emits incomplete tool call |
| Multiple interleaved tool calls | Each index tracked independently |

## Summary

The fix is straightforward - add the same tool call buffer pattern used in `handleInternalStream()` and `handleStreamingResponse()` to the `streamResponse()` function in `handler_external.go`.

This ensures consistent behavior across all ultimate model execution paths:
- Internal providers: Tool call buffer ✓
- External upstream: Tool call buffer ✓ (after fix)
