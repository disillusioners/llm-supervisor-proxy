# Tool Call Buffering Mode - Implementation Plan

## Problem Statement

Some LLM clients have weak streaming parsers that cannot handle incremental tool call fragments. They expect:
- Each SSE chunk contains a **complete, valid** tool call object
- `arguments` field contains valid, parseable JSON

Current proxy behavior:
- Streams tool call fragments as they arrive from upstream
- Each chunk may contain partial JSON in `arguments` field
- Post-stream repair only fixes the buffer, not what client already received

Example of problematic streaming:
```
Chunk 1: {"tool_calls":[{"index":0,"function":{"arguments":"{\"caseSensitive\":"}}]}
Chunk 2: {"tool_calls":[{"index":0,"function":{"arguments":" false,"}}]}
Chunk 3: {"tool_calls":[{"index":0,"function":{"arguments":"\"include\": \"*.go\"}"}]}
```

Weak clients try to `JSON.parse()` each chunk's `arguments` and fail.

## Proposed Solution

When enabled, the proxy will:
1. **Intercept** tool call chunks during streaming
2. **Buffer** fragments by index until arguments form valid JSON
3. **Emit** complete tool calls immediately when valid

### Output Behavior

**Emit each tool call individually as soon as complete:**

```
Chunk 1: content "Let me search..."        → emit immediately
Chunk 2: tool_calls[0] fragment 1          → buffer
Chunk 3: tool_calls[0] fragment 2          → buffer (now complete)
                                             → EMIT tool_calls[0] complete
Chunk 4: tool_calls[1] fragment 1          → buffer
Chunk 5: tool_calls[1] fragment 2          → buffer (now complete)
                                             → EMIT tool_calls[1] complete
Chunk 6: content "Found results..."        → emit immediately
```

**Example complete chunk:**
```json
data: {"tool_calls":[{"id":"call_x","type":"function","index":0,"function":{"name":"grep","arguments":"{\"caseSensitive\":false}"}}]}

data: {"tool_calls":[{"id":"call_y","type":"function","index":1,"function":{"name":"read_file","arguments":"{\"path\":\"test.go\"}"}}]}
```

---

## Architecture

### Current Flow

```
Upstream Stream → Normalizer → Accumulator → Buffer → Client
                                    ↓
                              Post-stream repair
```

### New Flow (Tool Call Buffering Enabled)

```
Upstream Stream → Normalizer → ToolCallBuffer → Buffer → Client
                                    ↓
                              Buffer fragments until valid JSON
                              Emit complete tool call immediately
```

### Component: ToolCallBuffer

A new component that sits between the normalizer and the stream buffer:

```go
type ToolCallBuffer struct {
    mu              sync.Mutex
    builders        map[int]*ToolCallBuilder  // index → accumulated data
    totalSize       int64                     // tracked size for memory protection
    maxSize         int64                     // max buffered bytes
    modelID         string
    requestID       string
}

type ToolCallBuilder struct {
    ID         string
    Type       string
    Name       string
    Arguments  strings.Builder
    hasEmitted bool // track if we've already emitted this tool call
}
```

### Behavior

1. **Content chunks**: Pass through immediately (no buffering)
2. **Tool call chunks**: 
   - Extract fragment by index
   - Accumulate arguments string
   - Check if JSON is now valid
   - If valid: emit complete tool call chunk immediately
   - If not: continue buffering

3. **Stream end**: Flush all remaining buffered tool calls (complete or not)

---

## Implementation Plan

### Phase 1: Core Buffering Logic

#### 1.1 Create ToolCallBuffer component

**File:** `pkg/proxy/tool_call_buffer.go`

```go
package proxy

import (
    "encoding/json"
    "fmt"
    "sort"
    "strings"
    "sync"
    "time"
)

// ToolCallBuffer buffers tool call fragments until complete JSON is formed
type ToolCallBuffer struct {
    mu         sync.Mutex
    builders   map[int]*ToolCallBuilder
    totalSize  int64
    maxSize    int64
    modelID    string
    requestID  string
}

// ToolCallBuilder accumulates fragments for a single tool call
type ToolCallBuilder struct {
    ID         string
    Type       string
    Name       string
    Arguments  strings.Builder
    hasEmitted bool
}

// NewToolCallBuffer creates a new tool call buffer
func NewToolCallBuffer(maxSize int64, modelID, requestID string) *ToolCallBuffer {
    if maxSize <= 0 {
        maxSize = 1024 * 1024 // Default 1MB
    }
    return &ToolCallBuffer{
        builders:  make(map[int]*ToolCallBuilder),
        maxSize:   maxSize,
        modelID:   modelID,
        requestID: requestID,
    }
}
```

#### 1.2 Implement chunk processing

```go
// ProcessChunk processes a normalized SSE chunk
// Returns: chunks to emit (may be empty if buffering, or multiple if flushing)
func (b *ToolCallBuffer) ProcessChunk(line []byte) [][]byte {
    lineStr := strings.TrimSpace(string(line))
    
    // Skip empty lines and [DONE] markers - pass through
    if lineStr == "" || lineStr == "data: [DONE]" || lineStr == "[DONE]" {
        return [][]byte{line}
    }
    
    // Strip "data: " prefix if present
    data := line
    hasPrefix := strings.HasPrefix(lineStr, "data: ")
    if hasPrefix {
        data = []byte(strings.TrimPrefix(lineStr, "data: "))
    }
    
    // Try to parse as JSON
    var chunk map[string]interface{}
    if err := json.Unmarshal(data, &chunk); err != nil {
        // Not valid JSON, pass through
        return [][]byte{line}
    }
    
    // Check if this chunk has tool_calls
    choices, ok := chunk["choices"].([]interface{})
    if !ok || len(choices) == 0 {
        return [][]byte{line} // No choices, pass through
    }
    
    choice, ok := choices[0].(map[string]interface{})
    if !ok {
        return [][]byte{line}
    }
    
    delta, ok := choice["delta"].(map[string]interface{})
    if !ok {
        return [][]byte{line}
    }
    
    toolCalls, ok := delta["tool_calls"].([]interface{})
    if !ok || len(toolCalls) == 0 {
        return [][]byte{line} // No tool calls, pass through
    }
    
    // This chunk has tool calls - buffer them
    return b.processToolCallChunk(chunk, toolCalls, hasPrefix)
}

// processToolCallChunk buffers tool call fragments and returns complete chunks
func (b *ToolCallBuffer) processToolCallChunk(chunk map[string]interface{}, toolCalls []interface{}, hasPrefix bool) [][]byte {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    var toEmit []int
    
    for _, tc := range toolCalls {
        tcMap, ok := tc.(map[string]interface{})
        if !ok {
            continue
        }
        
        // Get index (default to 0 if missing)
        index, ok := tcMap["index"].(float64)
        if !ok {
            index = 0
        }
        idx := int(index)
        
        // Get or create builder
        builder, exists := b.builders[idx]
        if !exists {
            builder = &ToolCallBuilder{}
            b.builders[idx] = builder
        }
        
        // Accumulate metadata
        if id, ok := tcMap["id"].(string); ok && id != "" {
            builder.ID = id
        }
        if typ, ok := tcMap["type"].(string); ok && typ != "" {
            builder.Type = typ
        }
        
        // Accumulate function details
        if fn, ok := tcMap["function"].(map[string]interface{}); ok {
            if name, ok := fn["name"].(string); ok && name != "" {
                builder.Name = name
            }
            if args, ok := fn["arguments"].(string); ok {
                // Check size limit
                if b.totalSize+int64(len(args)) > b.maxSize {
                    // Buffer overflow - emit what we have
                    toEmit = append(toEmit, idx)
                    continue
                }
                builder.Arguments.WriteString(args)
                b.totalSize += int64(len(args))
            }
        }
        
        // Check if this tool call is now complete (valid JSON)
        if b.isComplete(idx) && !builder.hasEmitted {
            toEmit = append(toEmit, idx)
        }
    }
    
    // Emit complete tool calls
    var chunks [][]byte
    for _, idx := range toEmit {
        chunks = append(chunks, b.emitToolCall(idx))
        b.builders[idx].hasEmitted = true
    }
    
    return chunks
}

// isComplete checks if tool call arguments form valid JSON
func (b *ToolCallBuffer) isComplete(idx int) bool {
    builder, exists := b.builders[idx]
    if !exists {
        return false
    }
    
    args := builder.Arguments.String()
    if args == "" {
        return false
    }
    
    var js interface{}
    return json.Unmarshal([]byte(args), &js) == nil
}

// emitToolCall creates a complete SSE chunk for a tool call
func (b *ToolCallBuffer) emitToolCall(idx int) []byte {
    builder := b.builders[idx]
    
    chunk := map[string]interface{}{
        "id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
        "object":  "chat.completion.chunk",
        "created": time.Now().Unix(),
        "model":   b.modelID,
        "choices": []map[string]interface{}{
            {
                "index": 0,
                "delta": map[string]interface{}{
                    "tool_calls": []map[string]interface{}{
                        {
                            "index": idx,
                            "id":    builder.ID,
                            "type":  builder.Type,
                            "function": map[string]interface{}{
                                "name":      builder.Name,
                                "arguments": builder.Arguments.String(),
                            },
                        },
                    },
                },
            },
        },
    }
    
    data, _ := json.Marshal(chunk)
    return []byte("data: " + string(data))
}

// Flush emits all remaining buffered tool calls (called on stream end)
func (b *ToolCallBuffer) Flush() [][]byte {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    // Get sorted indices
    indices := make([]int, 0, len(b.builders))
    for idx := range b.builders {
        indices = append(indices, idx)
    }
    sort.Ints(indices)
    
    var chunks [][]byte
    for _, idx := range indices {
        builder := b.builders[idx]
        if !builder.hasEmitted {
            chunks = append(chunks, b.emitToolCall(idx))
            builder.hasEmitted = true
        }
    }
    
    return chunks
}
```

### Phase 2: Integration

#### 2.1 Configuration

**File:** `pkg/config/config.go`

```go
type Config struct {
    // ... existing fields ...
    
    // ToolCallBufferDisabled disables buffering of tool call fragments
    // When false (default), tool calls are buffered until complete JSON is formed
    // Set to true only if client can handle partial JSON in arguments
    ToolCallBufferDisabled bool `json:"tool_call_buffer_disabled"`
    
    // ToolCallBufferMaxSize is the max bytes to buffer per request
    ToolCallBufferMaxSize int64 `json:"tool_call_buffer_max_size"`
}
```

Environment variables:
- `TOOL_CALL_BUFFER_DISABLED=false` (default: false, meaning feature is ENABLED)
- `TOOL_CALL_BUFFER_MAX_SIZE=1048576` (default: 1MB)

#### 2.2 Race Executor Integration

**File:** `pkg/proxy/race_executor.go`

Modify `handleStreamingResponse()`:

```go
func handleStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, provider string) error {
    // ... existing setup ...
    
    // Create tool call buffer (enabled by default, disabled only if explicitly set)
    var toolCallBuffer *ToolCallBuffer
    if !cfg.ToolCallBufferDisabled {
        toolCallBuffer = NewToolCallBuffer(cfg.ToolCallBufferMaxSize, req.modelID, fmt.Sprintf("%d", req.id))
    }
    
    for {
        // ... read line ...
        
        if len(line) > 0 {
            // ... trim newlines ...
            
            // Apply normalization FIRST
            normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName(line, normCtx)
            // ... logging ...
            
            // Process through tool call buffer
            var chunksToEmit [][]byte
            if toolCallBuffer != nil {
                chunksToEmit = toolCallBuffer.ProcessChunk(normalizedLine)
            } else {
                chunksToEmit = [][]byte{normalizedLine}
            }
            
            // Add all chunks to buffer
            for _, chunk := range chunksToEmit {
                if !req.buffer.Add(chunk) {
                    return fmt.Errorf("buffer limit exceeded")
                }
            }
            
            // ... rest of existing logic ...
        }
        
        // ... read error handling ...
    }
    
    // Flush any remaining buffered tool calls
    if toolCallBuffer != nil {
        for _, chunk := range toolCallBuffer.Flush() {
            if !req.buffer.Add(chunk) {
                break
            }
        }
    }
    
    // ... existing post-stream repair ...
}
```

Same pattern for `handleInternalStream()`.

### Phase 3: Edge Cases

#### 3.1 Multiple Tool Calls (Interleaved)

```
Chunk 1: tool_calls[0] fragment  → buffer[0]
Chunk 2: tool_calls[1] fragment  → buffer[1]
Chunk 3: tool_calls[0] fragment  → buffer[0] now complete → EMIT[0]
Chunk 4: tool_calls[1] fragment  → buffer[1] now complete → EMIT[1]
```

Each index tracked independently, emitted when complete.

#### 3.2 Content + Tool Calls Mixed

```
Chunk 1: content "Hello"     → emit immediately
Chunk 2: tool_calls[0] frag  → buffer
Chunk 3: content " world"    → emit immediately
Chunk 4: tool_calls[0] frag  → buffer complete → EMIT[0]
```

Content passes through, tool calls buffered.

#### 3.3 Stream Timeout/End

On stream end or timeout:
- Call `Flush()` to emit all remaining buffered tool calls
- Client receives best-effort complete tool calls

#### 3.4 Memory Protection

- Track total buffered size
- If `maxSize` exceeded, emit what we have and log warning
- Default: 1MB per request

---

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `TOOL_CALL_BUFFER_DISABLED` | `false` | Set to `true` to disable tool call buffering |
| `TOOL_CALL_BUFFER_MAX_SIZE` | `1048576` | Max bytes to buffer (1MB) |

### Config File

```json
{
  "tool_call_buffer_disabled": false,
  "tool_call_buffer_max_size": 1048576
}
```

**Note:** Feature is **enabled by default**. Set `TOOL_CALL_BUFFER_DISABLED=true` only if you need raw fragment streaming for compatibility with clients that handle partial JSON.

---

## Complexity Assessment

### Difficulty: **Moderate**

**Reasons:**
1. **Existing infrastructure** - `ToolCallAccumulator` already does fragment accumulation
2. **Clear integration points** - Only 2 functions need modification
3. **Well-defined behavior** - OpenAI spec is clear on tool call format

**Challenges:**
1. **Ordering preservation** - Must emit tool calls in correct order relative to content
2. **Memory management** - Need to track and limit buffer size
3. **Testing** - Need comprehensive tests for edge cases

### Estimated Changes

| File | Type | Lines |
|------|------|-------|
| `pkg/proxy/tool_call_buffer.go` | **NEW** | ~200 |
| `pkg/proxy/race_executor.go` | Modify | ~30 |
| `pkg/config/config.go` | Modify | ~10 |
| `pkg/proxy/tool_call_buffer_test.go` | **NEW** | ~150 |

---

## Testing Strategy

### Unit Tests

1. **Fragment accumulation** - Test that fragments are correctly concatenated
2. **JSON detection** - Test valid/invalid JSON detection
3. **Emission** - Test correct chunk format
4. **Multiple indices** - Test interleaved tool calls
5. **Memory limits** - Test buffer overflow behavior
6. **Flush** - Test stream end behavior

### Test Case Example

```go
func TestToolCallBuffer_EmitWhenComplete(t *testing.T) {
    buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")
    
    // Fragment 1 - incomplete, should not emit
    chunks := buffer.ProcessChunk([]byte(
        `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`
    ))
    if len(chunks) != 0 {
        t.Errorf("Should not emit incomplete tool call, got %d chunks", len(chunks))
    }
    
    // Fragment 2 - now complete, should emit
    chunks = buffer.ProcessChunk([]byte(
        `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`
    ))
    if len(chunks) != 1 {
        t.Errorf("Should emit complete tool call, got %d chunks", len(chunks))
    }
    
    // Verify emitted chunk has complete JSON arguments
    var obj map[string]interface{}
    data := strings.TrimPrefix(string(chunks[0]), "data: ")
    if err := json.Unmarshal([]byte(data), &obj); err != nil {
        t.Fatalf("Invalid JSON: %v", err)
    }
    
    args := extractToolCallArgs(obj, 0)
    if args != `{"city":"Paris"}` {
        t.Errorf("Unexpected arguments: %s", args)
    }
}

func TestToolCallBuffer_ContentPassThrough(t *testing.T) {
    buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")
    
    // Content chunk should pass through immediately
    chunks := buffer.ProcessChunk([]byte(
        `data: {"choices":[{"delta":{"content":"Hello world"}}]}`
    ))
    if len(chunks) != 1 {
        t.Errorf("Content should pass through, got %d chunks", len(chunks))
    }
}

func TestToolCallBuffer_Flush(t *testing.T) {
    buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")
    
    // Add incomplete fragment
    buffer.ProcessChunk([]byte(
        `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{incomplete"}}]}}]}`
    ))
    
    // Flush should emit it anyway
    chunks := buffer.Flush()
    if len(chunks) != 1 {
        t.Errorf("Flush should emit buffered tool call, got %d chunks", len(chunks))
    }
}
```

---

## Summary

This implementation provides:
- **Compatibility** with weak clients that cannot parse partial JSON
- **Minimal latency** impact (emit as soon as valid JSON formed)
- **Memory safety** with configurable limits
- **Simple configuration** - enabled by default, single flag to disable

**Default behavior:** Tool call buffering is **ENABLED**. Set `TOOL_CALL_BUFFER_DISABLED=true` only if your client can handle partial JSON fragments.
