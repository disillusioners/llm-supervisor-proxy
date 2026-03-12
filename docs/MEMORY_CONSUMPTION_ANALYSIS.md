# Memory Consumption Analysis Report

**Date:** 2026-03-12  
**Issue:** Slow external upstream consuming 2GB+ for single requests  
**Severity:** Critical  
**Status:** ✅ FIXED (Oracle Reviewed)

---

## Executive Summary

**Root Cause Identified:** The `ThinkingStrategy` in loop detection accumulates thinking content **unboundedly** and is **never reset** during the request lifecycle, including across retries.

For reasoning models (o1, o3, deepseek-r1) with slow upstream responses, a single request can consume **gigabytes of memory**.

---

## Critical Issues Found

### 1. CRITICAL: ThinkingStrategy Memory Leak

**Location:** `pkg/loopdetection/strategy_thinking.go:26-27`

```go
type ThinkingStrategy struct {
    // ...
    accumulatedThinking strings.Builder  // GROWS UNBOUNDED
    thinkingTokenCount  int              // GROWS UNBOUNDED
}
```

**Problem:**
- `AddThinkingContent()` appends without limit (line 74)
- `Reset()` exists but is **NEVER CALLED** in production code
- For reasoning models, thinking content can be **megabytes per chunk**

**Impact:** A single slow reasoning model request can consume **2GB+** in thinking content alone.

---

### 2. CRITICAL: Detector.Reset() Doesn't Reset Strategies

**Location:** `pkg/loopdetection/detector.go:163-168`

```go
func (d *Detector) Reset() {
    d.mu.Lock()
    defer d.mu.Unlock()
    d.window = d.window[:0]  // Only clears window
    d.msgCounter = 0
    // DOES NOT RESET ThinkingStrategy.accumulatedThinking
}
```

**Problem:** Even if `Detector.Reset()` were called, it wouldn't clear the `ThinkingStrategy` internal state.

---

### 3. CRITICAL: LoopDetector Persists Across Retries Without Reset

**Location:** `pkg/proxy/handler_functions.go:773-793`

```go
if rc.loopDetector == nil {
    rc.loopDetector = loopdetection.NewDetector(ldCfg)
}
// No reset between retries - detector is reused with accumulated state
detector := rc.loopDetector
```

**Problem:** The detector is created once per request context and **reused across all retries** without resetting `ThinkingStrategy`.

---

### 4. CRITICAL: Double-Accumulation Bug in Analyze() ⚠️ NEW

**Location:** `pkg/loopdetection/strategy_thinking.go:82-86`

```go
func (s *ThinkingStrategy) Analyze(window []MessageContext) *DetectionResult {
    // Also check window messages for thinking content type
    for _, msg := range window {
        if msg.ContentType == "thinking" && len(msg.Content) > 0 {
            s.AddThinkingContent(msg.Content + " ")  // ⚠️ Adds to buffer AGAIN
        }
    }
    // ...
}
```

**Problem:** Thinking content is added **twice**:
1. Once via `detector.AddThinkingContent(newThinking)` in `handler_functions.go:902`
2. **Again** inside `Analyze()` from the window

If `Analyze()` is called multiple times (which it is), content gets **duplicated** in the buffer, causing **exponential memory growth**.

---

### 5. MEDIUM: streamBuffer Grows to 100MB

**Location:** `pkg/proxy/handler_functions.go:849-865`

```go
bufferLimit := rc.conf.MaxStreamBufferSize
if bufferLimit <= 0 {
    bufferLimit = 100 * 1024 * 1024 // 100MB hard cap
}
```

**Problem:** For slow upstreams, the entire response is buffered until `[DONE]`. This can hold **100MB per request**.

---

### 6. MEDIUM: accumulatedThinking Duplication

**Location:** `pkg/proxy/handler_helpers.go:46` and `pkg/loopdetection/strategy_thinking.go:26`

There are **TWO** separate buffers accumulating thinking content:
1. `requestContext.accumulatedThinking` (reset at line 767)
2. `ThinkingStrategy.accumulatedThinking` (NEVER reset)

This doubles memory consumption for thinking content.

---

## Memory Flow Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         SINGLE REQUEST                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ Upstream Stream (SLOW - takes minutes)                       │  │
│  │   ↓                                                          │  │
│  │ For each chunk:                                              │  │
│  │   1. rc.streamBuffer.Write(chunk)     → +KB per chunk       │  │
│  │   2. rc.accumulatedResponse.Write()   → +KB per chunk       │  │
│  │   3. rc.accumulatedThinking.Write()   → +KB per chunk       │  │
│  │   4. detector.AddThinkingContent()    → +KB per chunk ⚠️    │  │
│  │   5. Analyze() adds again via window  → +KB × 2 ⚠️⚠️        │  │
│  │                                          (NEVER RESET!)      │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ RETRY (idle timeout, error, etc.)                            │  │
│  │   ↓                                                          │  │
│  │   1. rc.streamBuffer.Reset()          → CLEARED ✓           │  │
│  │   2. rc.accumulatedResponse.Reset()   → CLEARED ✓           │  │
│  │   3. rc.accumulatedThinking.Reset()   → CLEARED ✓           │  │
│  │   4. ThinkingStrategy.Reset()         → NEVER CALLED        │  │
│  │      (accumulatedThinking continues growing)                  │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  Result after N retries:                                           │
│    - streamBuffer: 0-100MB (reset each retry)                      │
│    - ThinkingStrategy.accumulatedThinking: 2N × (thinking content) │
│      → Can reach GB for reasoning models!                          │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Memory Estimation for Reasoning Models

For a slow reasoning model request:

| Component | Size Estimate | Notes |
|-----------|---------------|-------|
| `streamBuffer` | Up to 100MB | Hard capped |
| `accumulatedResponse` | 10-50MB | Reset each retry |
| `accumulatedThinking` (requestContext) | 10-50MB | Reset each retry |
| `ThinkingStrategy.accumulatedThinking` | **UNBOUNDED** | Never reset + double-accumulation |
| `Detector.window` | ~1MB | Bounded by MessageWindow |

**Worst case with 3 retries on a reasoning model:**
- Thinking content per attempt: ~500MB
- Double-accumulation: 500MB × 2 = 1GB per attempt
- Across 3 retries (no reset): 1GB × 3 = **3GB**
- Plus other buffers: ~200MB
- **Total: 3.2GB+ per request**

---

## Recommended Fixes

### Fix 1: Reset ThinkingStrategy on Detector.Reset()

**File:** `pkg/loopdetection/detector.go`

```go
func (d *Detector) Reset() {
    d.mu.Lock()
    defer d.mu.Unlock()
    d.window = d.window[:0]
    d.msgCounter = 0
    
    // Reset all strategies that have accumulated state
    for _, s := range d.strategies {
        if resetter, ok := s.(interface{ Reset() }); ok {
            resetter.Reset()
        }
    }
}
```

---

### Fix 2: Call Detector.Reset() Between Retries

**File:** `pkg/proxy/handler_functions.go` (around line 770, after `rc.streamID = ""`)

```go
// Clear any previous buffer content from failed attempts
rc.streamBuffer.Reset()
rc.accumulatedResponse.Reset()
rc.accumulatedThinking.Reset()
// Reset stream ID caching to get fresh ID from new upstream
rc.streamIDSet = false
rc.streamID = ""

// Reset loop detector state between retries
if rc.loopDetector != nil {
    rc.loopDetector.Reset()
}
```

**Ordering:** Reset detector **after** streamBuffer but **before** creating `streamBuf := detector.NewStreamBuffer()` at line 795.

---

### Fix 3: Add Memory Limit to ThinkingStrategy (Defense in Depth)

**File:** `pkg/loopdetection/strategy_thinking.go`

Use **sliding tail** approach instead of arbitrary half-truncation:

```go
const maxThinkingBufferSize = 1 * 1024 * 1024 // 1MB limit

func (s *ThinkingStrategy) AddThinkingContent(text string) {
    // Check size limit before adding
    if s.accumulatedThinking.Len() >= maxThinkingBufferSize {
        // Keep only the tail (most recent content for analysis)
        current := s.accumulatedThinking.String()
        keepLen := maxThinkingBufferSize / 2 // Keep last 512KB
        if keepLen > len(current) {
            keepLen = len(current)
        }
        tail := current[len(current)-keepLen:]
        s.accumulatedThinking.Reset()
        s.accumulatedThinking.WriteString(tail)
        s.thinkingTokenCount = fingerprint.EstimateTokenCount(tail)
    }
    
    s.accumulatedThinking.WriteString(text)
    s.thinkingTokenCount += fingerprint.EstimateTokenCount(text)
}
```

---

### Fix 4: Remove Double-Accumulation in Analyze()

**File:** `pkg/loopdetection/strategy_thinking.go` (line 82-86)

**Option A:** Remove the window accumulation entirely (content already added via `AddThinkingContent`):

```go
func (s *ThinkingStrategy) Analyze(window []MessageContext) *DetectionResult {
    // REMOVED: Don't add window content again - it's already in accumulatedThinking
    // for _, msg := range window {
    //     if msg.ContentType == "thinking" && len(msg.Content) > 0 {
    //         s.AddThinkingContent(msg.Content + " ")
    //     }
    // }

    if s.thinkingTokenCount < s.thinkingMinTokens {
        return nil
    }
    // ... rest of function unchanged
}
```

**Option B:** Only add content from window if not already added (more complex, not recommended).

---

## Implementation Priority

| Priority | Fix | Impact | Risk | Effort |
|----------|-----|--------|------|--------|
| **P0** | Fix 1 + Fix 2 | Stops the leak | Low | Low |
| **P0** | Fix 4 (double-accumulation) | Reduces memory ~2x | Low | Low |
| **P1** | Fix 3 (memory limit) | Defense in depth | Low | Low |
| **P2** | Consolidate thinking buffers | Reduces memory ~2x | High | Medium |

---

## Design Decision: Loop Detection Across Retries

**Question:** Should loop detection span across retries?

**Answer:** **No, it should not.** Each retry is a fresh attempt - the model gets a new chance to respond. Accumulated thinking from a failed attempt is irrelevant since that content was never delivered to the client. Cross-retry detection would cause false positives.

The current design correctly isolates loop detection to a single stream attempt. The fix (calling `Reset()` between retries) maintains this design.

---

## Verification Steps

After implementing fixes:

1. **Memory profiling:** Run with `GODEBUG=gctrace=1` and monitor memory
2. **Load test:** Send slow reasoning model requests and observe memory growth
3. **Unit test:** Add test verifying `Detector.Reset()` clears all strategy state
4. **Integration test:** Verify memory doesn't grow beyond expected bounds during retries
5. **Monitoring:** Log `ThinkingStrategy.accumulatedThinking.Len()` periodically to catch future regressions

---

## Additional Notes

- The heartbeat mechanism (`startSSEHeartbeat`) is properly cleaned up with `defer` and `stopHeartbeat()`
- The `MonitoredReader` properly closes its goroutine and underlying reader
- Event bus has a bounded history (100 events) and properly manages subscribers
- The main leak is isolated to `ThinkingStrategy` and the lack of reset between retries
---

## Implementation Status

| Fix | File | Status | Description |
|-----|------|--------|-------------|
| Fix 1 | `pkg/loopdetection/detector.go` | ✅ Implemented | Reset strategies in Detector.Reset loop |
| Fix 2 | `pkg/proxy/handler_functions.go` | ✅ Implemented | Call detector.Reset between retries |
| Fix 3 | `pkg/loopdetection/strategy_thinking.go` | ✅ Implemented | 1MB memory limit with sliding tail |
| Fix 4 | `pkg/loopdetection/strategy_thinking.go` | ✅ Implemented | Removed double-accumulation in Analyze() |

---

## Remaining Work

| Item | Priority | Status |
|------|----------|--------|
| Unit test for Detector.Reset clearing ThinkingStrategy | Low | Not started |
| Update stale comment in handler_functions.go | low | Done |
| Consider adding `currentModel = ""` to ThinkingStrategy.Reset() | low | Optional |

| Consolidate thinking buffers | P2 | Future refactor | high | architectural change needed |
