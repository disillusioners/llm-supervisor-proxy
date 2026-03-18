# Streaming Tool Calls Implementation Issues Analysis

## Executive Summary

This document analyzes the Llm-supervisor-proxy's streaming tool calls implementation against the OpenAI Streaming Tool Calls Specification ([`docs/openai-streaming-tool-calls-spec.md`](openai-streaming-tool-calls-spec.md)).

## Summary of Findings
| Issue # | Severity | Status | Impact |
|--------|----------|--------|--------|
| Tool calls without `index` field are silently dropped | **HIGH** | Bug | Data loss |
| Duplicate index tracking in multiple locations | Medium | Technical debt | Maintenance burden |
| No validation of final tool call arguments | Medium | Quality issue | Silent failures |
| Deprecated per-chunk repair code still exists | Low | Technical debt | Confusion |
| No-ID index assignment may be incorrect | Medium | Bug | Incorrect ordering |

| Missing edge case handling for empty tool_calls array | Low | Bug | Potential issues |

---

## Issue 1: Tool Calls Without `index` Field Are Silently Dropped (HIGH)

### Problem
When a tool call chunk arrives without an `index` field, the system **silently drops** the tool call data instead of handling it gracefully.
### Evidence
**File: [`pkg/proxy/tool_call_accumulator.go:95-99`](pkg/proxy/tool_call_accumulator.go:95)**
```go
// Get index (required for streaming)
index, ok := tcMap["index"].(float64)
if !ok {
    continue // <-- SILENTLY SKIPS THE entire tool call!
}
```
**File: [`pkg/proxy/handler_helpers.go:326-330`](pkg/proxy/handler_helpers.go:326)**
```go
// Get index (required for streaming)
index, ok := tcMap["index"].(float64)
if !ok {
    continue // <-- SAME ISSUE
}
```
### Why This Is a Problem
According to the spec (Section 11 - Compatibility Issues):
> Not all "OpenAI-compatible" APIs follow this spec:
> | Provider | Issue |
> |----------|-------|
> | Gemini (compat) | missing `index` |
> | Ollama | sometimes no `id` |
> | Some proxies | send full `args` in one chunk |
> **👉 So production parser should:**
> - Fallback if `index` missing → assume `0`
### Current Behavior
When a provider like Gemini sends tool calls without an `index` field:
1. The normalizer ([`FixMissingToolCallIndexNormalizer`](pkg/proxy/normalizers/tool_call_index.go)) adds the index
2. But if the normalizer fails or is disabled, the accumulator silently drops the data
3. No error is logged, making debugging difficult
### Recommended Fix
```go
// Fallback to index 0 if missing (per spec section 11)
index, ok := tcMap["index"].(float64)
if !ok {
    index = 0 // Default fallback for compatibility
    log.Printf("[WARN] Tool call missing index field, defaulting to 0")
}
idx := int(index)
```

---

## Issue 2: Duplicate Index Tracking Logic (MEDIUM)
### Problem
Index tracking for tool calls is implemented in **multiple places** with slightly different logic, creating maintenance burden and potential inconsistency.
### Locations
1. **Normalizer**: [`pkg/proxy/normalizers/tool_call_index.go`](pkg/proxy/normalizers/tool_call_index.go)
   - Tracks `SeenToolCallIDs` in `NormalizeContext`
   - Assigns index based on tool call ID
   
2. **Internal Stream Handler**: [`pkg/proxy/race_executor.go:221-222`](pkg/proxy/race_executor.go:221)
   ```go
   nextToolCallIndex := 0
   seenToolCallIDs := make(map[string]int)
   ```
   
3. **Accumulator**: [`pkg/proxy/tool_call_accumulator.go`](pkg/proxy/tool_call_accumulator.go)
   - Uses index from chunk directly (no tracking)
### Why This Is a Problem
- **Inconsistency risk**: Different parts of the code may assign different indices to the same tool call
- **Maintenance burden**: Changes to index logic must be made in multiple places
- **Testing complexity**: Need to verify behavior across multiple implementations
### Recommended Fix
Consolidate index tracking into a single source of truth:
```go
// Create a unified ToolCallIndexTracker interface
type ToolCallIndexTracker interface {
    GetOrCreateIndex(toolCallID string) int
    Reset()
}
```

---

## Issue 3: No Validation of Final Tool Call Arguments (MEDIUM)
### Problem
After accumulating tool call arguments across all chunks, there is no validation that the final concatenated string is valid JSON.
### Evidence
**File: [`pkg/proxy/handler.go:596-601`](pkg/proxy/handler.go:596)**
```go
// Finalize tool call arguments from builders
for i := range rc.accumulatedToolCalls {
    if i < len(rc.toolCallArgBuilders) {
        rc.accumulatedToolCalls[i].Function.Arguments = rc.toolCallArgBuilders[i].String()
    }
}
// No validation here!
```
### Why This Is a Problem
- Invalid JSON in tool call arguments will be passed to the client
- No warning or error is logged
- Debugging becomes difficult when tools fail
### Recommended Fix
Add validation after finalizing arguments:
```go
// Finalize and validate tool call arguments
for i := range rc.accumulatedToolCalls {
    if i < len(rc.toolCallArgBuilders) {
        args := rc.toolCallArgBuilders[i].String()
        rc.accumulatedToolCalls[i].Function.Arguments = args
        
        // Validate JSON
        if args != "" && !isValidJSON(args) {
            log.Printf("[WARN] Tool call[%d] has invalid JSON arguments: %.100s...", 
                i, args)
        }
    }
}
```

---

## Issue 4: Deprecated Per-Chunk Repair Code Still Exists (LOW)
### Problem
The codebase contains deprecated repair code that is no longer used but still present, causing confusion.
### Evidence
**File: [`pkg/proxy/normalizers/tool_call_repair.go:10-22`](pkg/proxy/normalizers/tool_call_repair.go:10)**
```go
// DEPRECATED: This normalizer is broken for streaming responses because tool call
// arguments are incrementally streamed across multiple chunks. Per-chunk repair cannot
// work because each chunk contains partial JSON that cannot be meaningfully repaired.
```
**File: [`pkg/proxy/race_executor.go:722-831`](pkg/proxy/race_executor.go:722)**
```go
// DEPRECATED: This function is broken for streaming responses...
// Use repairAccumulatedArgs() instead...
func repairToolCallArgumentsInChunk(line []byte, config toolrepair.Config) ([]byte, bool) {
```
### Why This Is a Problem
- **Code clutter**: Dead code makes the codebase harder to navigate
- **Confusion**: Developers might accidentally use the deprecated functions
- **Maintenance burden**: Tests still exist for deprecated code
### Recommended Fix
Remove deprecated code or clearly mark as "DO NOT USE":
```go
// Deprecated: Use repairAccumulatedArgs instead
// This function will be removed in a future version.
func repairToolCallArgumentsInChunk(...) {...}
```

---

## Issue 5: No-ID Index Assignment May Be Incorrect (MEDIUM)
### Problem
When tool calls arrive without an `id` field, the index assignment logic may produce incorrect results.
### Evidence
**File: [`pkg/proxy/normalizers/tool_call_index.go:89-95`](pkg/proxy/normalizers/tool_call_index.go:89)**
```go
// Get the tool call ID
id, hasID := tcMap["id"].(string)
if !hasID || id == "" {
    // No ID, can't track - assign index based on position in array
    tcMap["index"] = i
    modified = true
    continue
}
```
### Why This Is a Problem
Consider this scenario:
```
Chunk 1: {"tool_calls": [{"function": {"arguments": "{"}}]} // No ID, position 0
Chunk 2: {"tool_calls": [{"function": {"arguments": "\"city\":"}}]} // No ID, position 0
Chunk 3: {"tool_calls": [{"function": {"arguments": "\"Paris\"}"}}]} // No ID, position 0
```
All three chunks have assigned index 0 because they're all at position 0 in their respective arrays. But they should be the SAME tool call, so they should all have the same index.
However, without an ID, we cannot know if they're the same tool call or different ones.
### Current Behavior
- Each chunk assigns index based on position in that chunk's array
- No correlation between chunks
- May work correctly if there's only one tool call per chunk
- Will fail if multiple tool calls are interleaved without IDs
### Recommended Fix
For providers that don't send IDs, use a heuristic:
```go
// If no ID, try to correlate by function name if available
if !hasID || id == "" {
    if fn, ok := tcMap["function"].(map[string]interface{}); ok {
        if name, ok := fn["name"].(string); ok && name != "" {
            // Use function name as identifier
            id = "fn_" + name
        }
    }
    // Fall back to position-based index
    tcMap["index"] = i
}
```

---

## Issue 6: Missing Edge Case Handling (LOW)
### Problem
Empty `tool_calls` array is not explicitly handled.
### Evidence
**File: [`pkg/proxy/tool_call_accumulator.go:78-81`](pkg/proxy/tool_call_accumulator.go:78)**
```go
toolCalls, ok := delta["tool_calls"].([]interface{})
if !ok || len(toolCalls) == 0 {
    return nil
}
```
### Why This Is a Problem
While this doesn't cause a bug (empty arrays are correctly skipped), it's not explicitly documented whether this is expected behavior or an edge case that should be logged for debugging.
### Recommended Fix
Add debug logging for edge cases:
```go
toolCalls, ok := delta["tool_calls"].([]interface{})
if !ok {
    return nil // No tool_calls field, normal
}
if len(toolCalls) == 0 {
    // Empty array - this is unusual but valid
    log.Printf("[DEBUG] Received empty tool_calls array")
    return nil
}
```

---

## Architecture Assessment
### What Works Well
1. **Post-stream repair architecture**: The accumulate → repair → rewrite pattern correctly handles incremental tool call arguments.
2. **Split concatenated chunks normalizer**: Handles MiniMax-style malformed responses well.
3. **Empty role fix normalizer**: Correctly handles empty role fields.
4. **Thread-safe accumulator**: Uses mutex properly for concurrent access.
5. **Deadline-based repair skip**: Good trade-off between latency and correctness.

### Data Flow Diagram
```mermaid
graph TD
    A[Upstream Provider] -->|B[Normalizer]
    B -->|C[Accumulator]
    C -->|D[Buffer]
    D -->|E{Stream Complete?}
    E -->|Yes| F[Repair Check]
    F -->|Needs Repair?| G[Repair Tool Calls]
    G -->|H[Rewrite Buffer]
    H -->|I[Stream to Client]
    E -->|No| I
```

### Key Components
| Component | File | Purpose |
|-----------|------|---------|
| Normalizer Registry | `normalizers/registry.go` | Applies normalization rules to chunks |
| Fix Missing Index | `normalizers/tool_call_index.go` | Adds missing `index` field |
| Split Concatenated | `normalizers/split_concatenated.go` | Handles MiniMax bug |
| Tool Call Accumulator | `tool_call_accumulator.go` | Accumulates arguments across chunks |
| Buffer Rewriter | `buffer_rewriter.go` | Rewrites buffer with repaired args |
| Race Executor | `race_executor.go` | Orchestrates streaming and repair |

---

## Test Coverage Gaps
The following scenarios need more test coverage:
1. **Tool call without `index` field**: Should default to 0, currently dropped
2. **Tool call without `id` field across multiple chunks**: Index assignment may be wrong
3. **Interleaved parallel tool calls**: Chunks arriving out of order
4. **Provider that sends full arguments in one chunk**: Should work, needs test
5. **Repair timeout during high load**: Verify deadline behavior
6. **Empty `tool_calls` array**: Explicit test needed

---

## Recommended Action Items
| Priority | Issue | Effort | Impact |
|----------|-------|--------|--------|
| **P0** | Fix index missing fallback (Issue 1) | Small | High - Prevents data loss |
| **P1** | Add validation logging (Issue 3) | Small | Medium - Better debugging |
| **P1** | Consolidate index tracking (Issue 2) | Medium | Medium - Reduce technical debt |
| **P2** | Remove deprecated code (Issue 4) | Small | Low - Cleaner codebase |
| **P2** | Improve no-ID index assignment (Issue 5) | Medium | Medium - Better compatibility |
| **P3** | Add edge case logging (Issue 6) | Small | Low - Better observability |

---

## Appendix: Reference Algorithm from Spec
From the OpenAI spec, Section 8:
```javascript
const toolCalls = {}

for (chunk of stream) {
  const delta = chunk.choices[0].delta

  if (!delta.tool_calls) continue

  for (tc of delta.tool_calls) {
    const i = tc.index

    if (!toolCalls[i]) {
      toolCalls[i] = {
        id: null,
        type: "function",
        function: {
          name: "",
          arguments: ""
        }
      }
    }

    if (tc.id) toolCalls[i].id = tc.id
    if (tc.function?.name) toolCalls[i].function.name = tc.function.name
    if (tc.function?.arguments)
      toolCalls[i].function.arguments += tc.function.arguments
  }
}
```
**Key difference**: The reference algorithm assumes `index` is always present. Our implementation needs to handle the case where it's missing (compatibility fallback).
