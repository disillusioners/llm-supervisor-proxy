# Memory Leak Fix: O(n²) String Growth in Stream Handler

**Date**: 2026-03-15  
**File**: `pkg/proxy/handler_response.go`  
**Severity**: Critical — single request can allocate GBs of memory

---

## Problem

The proxy's stream handler (`handleStreamResponse`) had a **quadratic memory growth** bug that caused memory usage to suddenly jump to gigabytes during normal streaming requests.

### Root Cause

On every SSE chunk received from upstream, the code extracted new content by calling `String()` on the full accumulated response and then slicing it:

```go
// BEFORE (buggy) — called on EVERY SSE chunk
prevLen := rc.accumulatedResponse.Len()
extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls)
newContent := rc.accumulatedResponse.String()[prevLen:]
newThinking := rc.accumulatedThinking.String()[prevThinkLen:]
```

`strings.Builder.String()` returns the **entire accumulated string** as a new allocation every time it's called. The `[prevLen:]` slice creates a substring that keeps the full backing array alive in memory (Go strings are immutable and share backing arrays with their parent).

This means:
- **Chunk 1**: `String()` allocates ~100 bytes
- **Chunk 100**: `String()` allocates ~10KB
- **Chunk 1,000**: `String()` allocates ~100KB  
- **Chunk 5,000**: `String()` allocates ~500KB

All of these allocations pile up because the GC can't reclaim them fast enough in the tight loop.

### Memory Impact

For a typical agentic coding response (~500KB, ~5,000 SSE chunks):

| Chunk # | `String()` allocates | Cumulative temp memory |
|---------|---------------------|----------------------|
| 1       | ~100 bytes          | ~100 bytes           |
| 100     | ~10KB               | ~500KB               |
| 1,000   | ~100KB              | ~50MB                |
| 5,000   | ~500KB              | **~1.25GB**          |

Total temporary allocations: **sum(1..N) × chunk_size ≈ O(n²)**

This explains why memory "suddenly jumps to GB" — it's not a traditional leak (retained references) but rather an allocation rate that overwhelms the garbage collector during the streaming loop.

---

## Fix

Extract chunk content into **per-chunk temporary builders** instead of slicing the accumulated string. The temporary builders are small (single chunk worth of content) and immediately eligible for GC after each loop iteration.

```go
// AFTER (fixed) — O(n) memory, no quadratic growth
var chunkResponse, chunkThinking strings.Builder
extractStreamChunkContent(data, &chunkResponse, &chunkThinking, &rc.accumulatedToolCalls)
newContent := chunkResponse.String()   // Small string (~100 bytes), immediately GC-eligible
newThinking := chunkThinking.String()
if newContent != "" {
    rc.accumulatedResponse.WriteString(newContent)
}
if newThinking != "" {
    rc.accumulatedThinking.WriteString(newThinking)
}
```

### Why This Works

- `chunkResponse` is a new `strings.Builder` scoped to each loop iteration
- `chunkResponse.String()` returns only the **current chunk's content** (~100 bytes), not the full accumulated response (~500KB)
- The per-chunk string is immediately eligible for garbage collection after the loop iteration
- `rc.accumulatedResponse.WriteString()` appends to the builder's internal buffer without creating intermediate string copies
- Total memory growth is now **O(n)** — linear with response size

### Diff

```diff
-// Track chunk content for both existing accumulation and loop detection
-prevLen := rc.accumulatedResponse.Len()
-prevThinkLen := rc.accumulatedThinking.Len()
-extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls)
-newContent := rc.accumulatedResponse.String()[prevLen:]
-newThinking := rc.accumulatedThinking.String()[prevThinkLen:]
+// Extract chunk content into temporary builders to avoid O(n²) memory growth.
+// Previously: accumulatedResponse.String()[prevLen:] was called on every chunk,
+// which copies the FULL accumulated string each time (quadratic memory).
+// Now: extract new content directly from this chunk, then append to accumulators.
+var chunkResponse, chunkThinking strings.Builder
+extractStreamChunkContent(data, &chunkResponse, &chunkThinking, &rc.accumulatedToolCalls)
+newContent := chunkResponse.String()
+newThinking := chunkThinking.String()
+if newContent != "" {
+    rc.accumulatedResponse.WriteString(newContent)
+}
+if newThinking != "" {
+    rc.accumulatedThinking.WriteString(newThinking)
+}
```

---

## Verification

- Build: `go build ./...` ✅
- All streaming tests pass (`TestMockLLM_NormalStreaming`, `TestMockLLM_ThinkingStream`, `TestMockLLM_LongStream`, `TestBufferedStreaming_BodyBufferedUntilDone`, etc.) ✅
- Behavior unchanged: `newContent` and `newThinking` variables still contain the same per-chunk content for loop detection
