# Streaming Tool Calls Implementation Fix Plan

## Executive Summary

This document provides a detailed implementation plan to address the issues identified in the streaming tool call implementation review. The fixes are prioritized by severity and impact.

**Created:** 2026-03-18
**Status:** Ready for Implementation

---

## Priority Classification

| Priority | Description | Issues |
|----------|-------------|--------|
| **P0** | Critical - Data loss potential | 1, 7 |
| **P1** | High - Stability/debugging | 3, 11 |
| **P2** | Medium - Technical debt | 2, 5, 10 |
| **P3** | Low - Code quality | 4, 6, 8, 9, 12, 13 |

---

## P0: Critical Fixes

### Fix 1: Add Index Fallback in ToolCallAccumulator

**Issue:** Tool calls without `index` field are silently dropped in [`tool_call_accumulator.go:95-98`](../pkg/proxy/tool_call_accumulator.go:95)

**Current Code:**
```go
// Get index (required for streaming)
index, ok := tcMap["index"].(float64)
if !ok {
    continue // <-- SILENTLY SKIPS the entire tool call!
}
```

**Fix:**
```go
// Get index (fallback to 0 if missing per OpenAI spec section 11)
index, ok := tcMap["index"].(float64)
if !ok {
    log.Printf("[WARN] Tool call missing index field, defaulting to 0")
    index = 0
}
```

**Files to Modify:**
- [`pkg/proxy/tool_call_accumulator.go`](../pkg/proxy/tool_call_accumulator.go) - Line 95-98

---

### Fix 2: Add Index Fallback in handler_helpers.go

**Issue:** Same silent drop issue in [`handler_helpers.go:326-329`](../pkg/proxy/handler_helpers.go:326)

**Current Code:**
```go
// Get index (required for streaming)
index, ok := tcMap["index"].(float64)
if !ok {
    continue
}
```

**Fix:**
```go
// Get index (fallback to 0 if missing per OpenAI spec section 11)
index, ok := tcMap["index"].(float64)
if !ok {
    log.Printf("[WARN] Tool call missing index field in extractStreamChunkContent, defaulting to 0")
    index = 0
}
```

**Files to Modify:**
- [`pkg/proxy/handler_helpers.go`](../pkg/proxy/handler_helpers.go) - Line 326-329

---

### Fix 3: Add Index Fallback in buffer_rewriter.go

**Issue:** Buffer rewriter drops tool calls without index in [`buffer_rewriter.go:106-110`](../pkg/proxy/buffer_rewriter.go:106)

**Current Code:**
```go
// Get index
index, ok := tcMap["index"].(float64)
if !ok {
    continue // <-- SILENTLY SKIPS
}
```

**Fix:**
```go
// Get index (fallback to 0 if missing)
index, ok := tcMap["index"].(float64)
if !ok {
    log.Printf("[WARN] Tool call missing index in buffer rewrite, defaulting to 0")
    index = 0
}
```

**Files to Modify:**
- [`pkg/proxy/buffer_rewriter.go`](../pkg/proxy/buffer_rewriter.go) - Line 106-110

---

## P1: High Priority Fixes

### Fix 4: Add JSON Validation Logging for Tool Call Arguments

**Issue:** No validation of final tool call arguments in [`handler.go:596-601`](../pkg/proxy/handler.go:596)

**Current Code:**
```go
// Finalize tool call arguments from builders
for i := range rc.accumulatedToolCalls {
    if i < len(rc.toolCallArgBuilders) {
        rc.accumulatedToolCalls[i].Function.Arguments = rc.toolCallArgBuilders[i].String()
    }
}
```

**Fix:**
```go
// Finalize tool call arguments from builders
for i := range rc.accumulatedToolCalls {
    if i < len(rc.toolCallArgBuilders) {
        args := rc.toolCallArgBuilders[i].String()
        rc.accumulatedToolCalls[i].Function.Arguments = args
        
        // Validate JSON arguments
        if args != "" {
            var js interface{}
            if err := json.Unmarshal([]byte(args), &js); err != nil {
                log.Printf("[WARN] Tool call[%d] has invalid JSON arguments: %v (args length: %d)", 
                    i, err, len(args))
            }
        }
    }
}
```

**Files to Modify:**
- [`pkg/proxy/handler.go`](../pkg/proxy/handler.go) - Line 596-601

---

### Fix 5: Add Max Tool Call Count Limit

**Issue:** No limit on tool call count or index value in [`tool_call_accumulator.go:101-105`](../pkg/proxy/tool_call_accumulator.go:101)

**Current Code:**
```go
// Ensure we have a builder for this index
if _, exists := a.args[idx]; !exists {
    a.args[idx] = &strings.Builder{}
    a.metadata[idx] = ToolCallMeta{}
}
```

**Fix - Add Constants:**
```go
const (
    // MaxToolCallsPerStream limits the number of tool calls per streaming response
    // to prevent memory exhaustion from malicious or buggy upstreams
    MaxToolCallsPerStream = 100
    
    // MaxToolCallIndex limits the maximum index value to prevent sparse array attacks
    MaxToolCallIndex = 99
    
    // MaxToolCallArgsSize limits the total size of tool call arguments per index
    MaxToolCallArgsSize = 1024 * 1024 // 1MB per tool call
)
```

**Fix - Add Validation:**
```go
// Validate index bounds
if idx < 0 || idx > MaxToolCallIndex {
    log.Printf("[WARN] Tool call index %d out of bounds (max: %d), skipping", idx, MaxToolCallIndex)
    continue
}

// Check max tool call count
if len(a.args) >= MaxToolCallsPerStream {
    log.Printf("[WARN] Max tool call count (%d) exceeded, skipping index %d", MaxToolCallsPerStream, idx)
    continue
}

// Ensure we have a builder for this index
if _, exists := a.args[idx]; !exists {
    a.args[idx] = &strings.Builder{}
    a.metadata[idx] = ToolCallMeta{}
}

// Check argument size limit before writing
if a.args[idx].Len()+len(args) > MaxToolCallArgsSize {
    log.Printf("[WARN] Tool call[%d] arguments exceed size limit (%d bytes), truncating", idx, MaxToolCallArgsSize)
    continue
}
```

**Files to Modify:**
- [`pkg/proxy/tool_call_accumulator.go`](../pkg/proxy/tool_call_accumulator.go) - Add constants and validation

---

## P2: Medium Priority Fixes

### Fix 6: Consolidate Index Tracking Logic

**Issue:** Duplicate index tracking in 3 locations:
1. [`pkg/proxy/normalizers/tool_call_index.go`](../pkg/proxy/normalizers/tool_call_index.go)
2. [`pkg/proxy/race_executor.go:221-222`](../pkg/proxy/race_executor.go:221)
3. [`pkg/proxy/tool_call_accumulator.go`](../pkg/proxy/tool_call_accumulator.go)

**Recommendation:** Create a centralized `ToolCallIndexTracker` utility:

**New File: `pkg/proxy/tool_call_index_tracker.go`**
```go
package proxy

import "sync"

// ToolCallIndexTracker provides thread-safe tool call index tracking
// to ensure consistent index assignment across normalizers and accumulators
type ToolCallIndexTracker struct {
    mu              sync.RWMutex
    seenIDs         map[string]int
    nextIndex       int
    maxIndex        int
}

// NewToolCallIndexTracker creates a new tracker
func NewToolCallIndexTracker(maxIndex int) *ToolCallIndexTracker {
    return &ToolCallIndexTracker{
        seenIDs:  make(map[string]int),
        nextIndex: 0,
        maxIndex: maxIndex,
    }
}

// GetOrCreateIndex returns the index for a tool call ID
// If the ID hasn't been seen, assigns the next available index
func (t *ToolCallIndexTracker) GetOrCreateIndex(id string) (int, bool) {
    if id == "" {
        return -1, false
    }
    
    t.mu.RLock()
    if idx, exists := t.seenIDs[id]; exists {
        t.mu.RUnlock()
        return idx, true
    }
    t.mu.RUnlock()
    
    t.mu.Lock()
    defer t.mu.Unlock()
    
    // Double-check after acquiring write lock
    if idx, exists := t.seenIDs[id]; exists {
        return idx, true
    }
    
    if t.nextIndex > t.maxIndex {
        return -1, false
    }
    
    idx := t.nextIndex
    t.seenIDs[id] = idx
    t.nextIndex++
    return idx, true
}

// Reset clears the tracker for reuse
func (t *ToolCallIndexTracker) Reset() {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.seenIDs = make(map[string]int)
    t.nextIndex = 0
}
```

**Migration Plan:**
1. Create the new tracker utility
2. Update `NormalizeContext` to use the tracker
3. Update `race_executor.go` to use the tracker
4. Update `tool_call_accumulator.go` to use the tracker (optional, as it reads index from chunks)

---

### Fix 7: Improve No-ID Index Assignment

**Issue:** Index assignment based on position may be incorrect for interleaved tool calls without IDs in [`tool_call_index.go:89-95`](../pkg/proxy/normalizers/tool_call_index.go:89)

**Current Code:**
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

**Fix:**
```go
// Get the tool call ID
id, hasID := tcMap["id"].(string)
if !hasID || id == "" {
    // No ID - try to match by function name if available
    // This handles the case where the same tool call is streamed across chunks
    // without IDs but with consistent function names
    if fn, ok := tcMap["function"].(map[string]interface{}); ok {
        if name, ok := fn["name"].(string); ok && name != "" {
            // Use function name as pseudo-ID
            pseudoID := "__fn__" + name
            if idx, seen := ctx.SeenToolCallIDs[pseudoID]; seen {
                tcMap["index"] = idx
            } else {
                idx := len(ctx.SeenToolCallIDs)
                ctx.SeenToolCallIDs[pseudoID] = idx
                tcMap["index"] = idx
            }
            modified = true
            continue
        }
    }
    
    // Fallback: assign index based on position in array
    log.Printf("[WARN] Tool call has no ID and no function name, using position-based index %d", i)
    tcMap["index"] = i
    modified = true
    continue
}
```

**Files to Modify:**
- [`pkg/proxy/normalizers/tool_call_index.go`](../pkg/proxy/normalizers/tool_call_index.go) - Line 89-95

---

### Fix 8: Add Ordering Enforcement Test

**Issue:** Normalizer/accumulator ordering is documented but not enforced in [`race_executor.go:946-950`](../pkg/proxy/race_executor.go:946)

**Add Test File: `pkg/proxy/normalizer_accumulator_order_test.go`**
```go
package proxy

import (
    "testing"
    
    "github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
)

// TestNormalizerRunsBeforeAccumulator verifies that normalization
// happens BEFORE accumulation in the streaming pipeline.
// This is critical because:
// 1. Normalizers fix malformed chunks (e.g., missing index)
// 2. Accumulators expect well-formed chunks
// 3. If order is reversed, data loss can occur
func TestNormalizerRunsBeforeAccumulator(t *testing.T) {
    // Create a chunk with missing index
    chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"test"}}]}}]}`)
    
    // Create accumulator
    acc := NewToolCallAccumulator()
    
    // Create normalizer context
    normCtx := normalizers.NewContext("", "test")
    normalizers.GetRegistry().ResetAll(normCtx)
    
    // Step 1: Normalize FIRST
    normalized, modified, name := normalizers.NormalizeWithContextAndName(chunk, normCtx)
    if !modified {
        t.Error("Expected normalizer to add missing index")
    }
    t.Logf("Normalizer %s modified the chunk", name)
    
    // Step 2: Accumulate AFTER normalization
    err := acc.ProcessChunk(normalized)
    if err != nil {
        t.Errorf("Accumulator failed after normalization: %v", err)
    }
    
    // Verify tool call was accumulated
    if !acc.HasToolCalls() {
        t.Error("Expected tool calls to be accumulated after normalization")
    }
    
    // Verify index was added
    meta := acc.GetMetadata()
    if len(meta) == 0 {
        t.Error("Expected metadata to have tool calls")
    }
}

// TestAccumulatorFailsWithoutNormalization proves that without
// normalization, tool calls without index would be dropped
func TestAccumulatorFailsWithoutNormalization(t *testing.T) {
    // Create a chunk with missing index (simulating broken upstream)
    chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"test"}}]}}]}`)
    
    // Create accumulator
    acc := NewToolCallAccumulator()
    
    // Process WITHOUT normalization
    err := acc.ProcessChunk(chunk)
    if err != nil {
        t.Logf("Accumulator returned error (expected): %v", err)
    }
    
    // Without normalization, tool call is dropped (index missing)
    // This test documents the current behavior that we're fixing
    if acc.HasToolCalls() {
        // After P0 fixes are applied, this should pass
        t.Log("Tool call was accumulated (P0 fix applied)")
    } else {
        t.Log("Tool call was dropped (P0 fix NOT applied)")
    }
}
```

**Files to Create:**
- [`pkg/proxy/normalizer_accumulator_order_test.go`](../pkg/proxy/normalizer_accumulator_order_test.go)

---

## P3: Low Priority Fixes

### Fix 9: Remove Deprecated Per-Chunk Repair Code

**Issue:** Deprecated code in [`race_executor.go:722-831`](../pkg/proxy/race_executor.go:722) and [`tool_call_repair.go:10-22`](../pkg/proxy/normalizers/tool_call_repair.go:10)

**Action:**
1. Mark functions with `// Deprecated:` comments (already done)
2. Add build tags to exclude from production builds
3. Remove in next major version

**Files to Modify:**
- [`pkg/proxy/race_executor.go`](../pkg/proxy/race_executor.go) - Consider removal of `repairToolCallArgumentsInChunk`
- [`pkg/proxy/normalizers/tool_call_repair.go`](../pkg/proxy/normalizers/tool_call_repair.go) - Mark for deprecation

---

### Fix 10: Add Debug Logging for Empty Tool Calls

**Issue:** Empty `tool_calls` array handling in [`tool_call_accumulator.go:78-81`](../pkg/proxy/tool_call_accumulator.go:78)

**Current Code:**
```go
toolCalls, ok := delta["tool_calls"].([]interface{})
if !ok || len(toolCalls) == 0 {
    return nil
}
```

**Fix:**
```go
toolCalls, ok := delta["tool_calls"].([]interface{})
if !ok {
    // tool_calls field is not an array - this is unexpected
    log.Printf("[DEBUG] Tool calls field is not an array: %T", delta["tool_calls"])
    return nil
}
if len(toolCalls) == 0 {
    // Empty array is valid, but log for debugging
    return nil
}
```

**Files to Modify:**
- [`pkg/proxy/tool_call_accumulator.go`](../pkg/proxy/tool_call_accumulator.go) - Line 78-81

---

### Fix 11: Add Type Field Validation

**Issue:** No validation of `type` field (should be `"function"`)

**Fix - Add validation in tool_call_accumulator.go:**
```go
// Validate type field if present
if typ, ok := tcMap["type"].(string); ok && typ != "" && typ != "function" {
    log.Printf("[WARN] Tool call has unexpected type: %s (expected 'function')", typ)
}
```

**Files to Modify:**
- [`pkg/proxy/tool_call_accumulator.go`](../pkg/proxy/tool_call_accumulator.go) - Add after line 114

---

### Fix 12: Add Finish Reason Validation

**Issue:** No validation of `finish_reason` field in [`race_executor.go:391-394`](../pkg/proxy/race_executor.go:391)

**Current Code:**
```go
finishReason := event.FinishReason
if finishReason == "" {
    finishReason = "stop"
}
```

**Fix:**
```go
finishReason := event.FinishReason
if finishReason == "" {
    finishReason = "stop"
}

// Validate finish_reason
validReasons := map[string]bool{"stop": true, "tool_calls": true, "length": true, "content_filter": true}
if !validReasons[finishReason] {
    log.Printf("[WARN] Invalid finish_reason: %s, defaulting to 'stop'", finishReason)
    finishReason = "stop"
}
```

**Files to Modify:**
- [`pkg/proxy/race_executor.go`](../pkg/proxy/race_executor.go) - Line 391-394

---

### Fix 13: Add Tool Call ID Uniqueness Validation

**Issue:** No validation of tool call ID uniqueness

**Fix - Add in handler.go after finalization:**
```go
// Check for duplicate tool call IDs
seenIDs := make(map[string]int)
for i, tc := range rc.accumulatedToolCalls {
    if tc.ID != "" {
        if firstIdx, exists := seenIDs[tc.ID]; exists {
            log.Printf("[WARN] Duplicate tool call ID '%s' at indices %d and %d", tc.ID, firstIdx, i)
        } else {
            seenIDs[tc.ID] = i
        }
    }
}
```

**Files to Modify:**
- [`pkg/proxy/handler.go`](../pkg/proxy/handler.go) - Add after line 601

---

### Fix 14: Add Function Name Validation

**Issue:** No validation that `function.name` is non-empty

**Fix - Add in handler.go after finalization:**
```go
// Validate function names are present
for i, tc := range rc.accumulatedToolCalls {
    if tc.Function.Name == "" {
        log.Printf("[WARN] Tool call[%d] has empty function name", i)
    }
}
```

**Files to Modify:**
- [`pkg/proxy/handler.go`](../pkg/proxy/handler.go) - Add after line 601

---

## Implementation Order

```
Phase 1 - Critical (P0)
├── Fix 1: tool_call_accumulator.go index fallback
├── Fix 2: handler_helpers.go index fallback
└── Fix 3: buffer_rewriter.go index fallback

Phase 2 - High Priority (P1)
├── Fix 4: handler.go JSON validation logging
└── Fix 5: tool_call_accumulator.go max limits

Phase 3 - Medium Priority (P2)
├── Fix 6: Consolidate index tracking
├── Fix 7: Improve no-ID index assignment
└── Fix 8: Add ordering enforcement test

Phase 4 - Low Priority (P3)
├── Fix 9: Remove deprecated code
├── Fix 10: Add empty tool_calls debug logging
├── Fix 11: Add type field validation
├── Fix 12: Add finish_reason validation
├── Fix 13: Add ID uniqueness validation
└── Fix 14: Add function name validation
```

---

## Testing Strategy

### Unit Tests

1. **Index Fallback Tests**
   - Test accumulator with missing index → defaults to 0
   - Test buffer rewriter with missing index → defaults to 0
   - Test handler_helpers with missing index → defaults to 0

2. **Max Limit Tests**
   - Test accumulator rejects index > MaxToolCallIndex
   - Test accumulator rejects count > MaxToolCallsPerStream
   - Test accumulator rejects args size > MaxToolCallArgsSize

3. **Validation Tests**
   - Test JSON validation logs warning for invalid args
   - Test type validation logs warning for non-"function"
   - Test finish_reason validation rejects invalid values

### Integration Tests

1. **End-to-End Streaming Test**
   - Stream with tool calls missing index
   - Verify tool calls are NOT dropped
   - Verify logs contain appropriate warnings

2. **Malformed Upstream Test**
   - Mock upstream sending malformed tool calls
   - Verify proxy handles gracefully

---

## Rollback Plan

If issues arise after deployment:

1. **P0 Fixes** can be reverted individually by restoring the `continue` statement
2. **P1 Fixes** are logging-only and don't affect behavior
3. **P2/P3 Fixes** are non-breaking and can be reverted if needed

---

## Metrics to Monitor

After implementation, monitor:

1. **Log Frequency**
   - Count of "missing index field" warnings
   - Count of "invalid JSON arguments" warnings
   - Count of "max tool call count exceeded" warnings

2. **Error Rates**
   - Tool call processing errors
   - Stream completion failures

3. **Performance**
   - Memory usage per request (should be bounded by limits)
   - Latency impact from validation

---

## Summary

| Phase | Fixes | Impact | Risk |
|-------|-------|--------|------|
| P0 | 3 | Prevents data loss | Low |
| P1 | 2 | Better debugging/stability | Low |
| P2 | 3 | Technical debt reduction | Medium |
| P3 | 6 | Code quality | Low |

**Total:** 14 fixes across 6 files
