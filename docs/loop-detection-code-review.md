# Loop Detection Implementation - Code Review

**Review Date**: 2026-02-22  
**Reviewer**: AI Code Review  
**Files Reviewed**: `pkg/loopdetection/` (entire package), `pkg/proxy/handler_functions.go` (integration)

---

## Executive Summary

The loop detection implementation is **well-architected and functional** for Phase 1 (Core Detection). All 19 tests pass. The code follows the plan document closely and uses shadow mode by default for safe rollout.

**Recommendation**: Address the high-priority issues before enabling in production.

---

## ✅ What's Done Well

### Architecture
- Clean separation of concerns: `types.go`, `config.go`, `detector.go`, strategies, buffer, fingerprint
- Strategy pattern allows easy extension
- Thread-safe with proper mutex locking

### Test Coverage
```
19 tests, all passing (0.004s)
- SimHash: identical, similar, different, empty text
- Trigram: high repetition, normal, short text
- Exact match: detected, not triggered
- Similarity: detected
- Action patterns: repeat, oscillation
- Stream buffer: threshold, message end, flush
- Config: disabled, shadow mode, reset, window size
```

### Safety-First Defaults
- `ShadowMode: true` by default - logs only, no interruption
- `MinTokensForSimHash: 15` - avoids false positives on short text
- `MinTokensForAnalysis: 20` - proper stream buffering

### Integration
- Clean integration into `handler_functions.go`
- Event publishing via existing event bus
- Non-blocking detection in stream processing loop

### Code Quality
- Good documentation on public functions
- Clean Go idioms throughout
- Proper use of `sync.Mutex` for concurrent safety

---

## 🔴 Issues to Address

### Issue 1: `AnalyzeActions` Creates Strategy Every Call (High Priority)

**File**: `pkg/loopdetection/detector.go:109`

```go
func (d *Detector) AnalyzeActions(actions []Action) *DetectionResult {
    // BUG: Creates new strategy on every call
    strategy := NewActionPatternStrategy(d.config.ActionRepeatCount, d.config.OscillationCount)
    window := []MessageContext{{Actions: actions}}
    result := strategy.Analyze(window)
    // ...
}
```

**Problem**: Memory allocation on every call. In a high-throughput proxy, this adds unnecessary GC pressure.

**Fix**:
```go
func (d *Detector) AnalyzeActions(actions []Action) *DetectionResult {
    d.mu.Lock()
    defer d.mu.Unlock()

    if !d.config.Enabled {
        return nil
    }

    // Reuse existing strategy from d.strategies
    for _, s := range d.strategies {
        if ap, ok := s.(*ActionPatternStrategy); ok {
            window := []MessageContext{{Actions: actions}}
            return ap.Analyze(window)
        }
    }
    return nil
}
```

---

### Issue 2: Trigram Analysis Implemented But Unused (Medium Priority)

**File**: `pkg/loopdetection/fingerprint/ngram.go`

```go
// TrigramRepetitionRatio is implemented but no strategy uses it
func TrigramRepetitionRatio(text string) float64 { ... }
```

**Problem**: Dead code. The plan specified thinking loop detection via trigrams, but no `ThinkingAnalyzer` strategy exists.

**Options**:
1. **Remove it** - If not needed for Phase 1
2. **Add `ThinkingStrategy`** - To detect repetitive reasoning patterns (Phase 3)
3. **Document as Phase 3** - Leave a TODO comment

---

### Issue 3: Tool Calls Not Extracted from Stream (High Priority)

**File**: `pkg/proxy/handler_functions.go:371-387`

```go
// Track chunk content for both existing accumulation and loop detection
prevLen := rc.accumulatedResponse.Len()
extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking)
newContent := rc.accumulatedResponse.String()[prevLen:]

// Feed content to loop detection buffer
if detector.IsEnabled() && len(newContent) > 0 {
    streamBuf.AddText(newContent)  // ✅ Text added
    
    // ❌ MISSING: Extract tool calls and call streamBuf.AddAction()
    // Actions are always nil!
}
```

**Problem**: `streamBuf.AddAction()` exists but is never called. Action pattern detection cannot work without tool call extraction.

**Fix**:
```go
if detector.IsEnabled() && len(newContent) > 0 {
    streamBuf.AddText(newContent)
    
    // Extract tool calls from SSE chunk
    if toolCalls := extractToolCallsFromChunk(data); len(toolCalls) > 0 {
        for _, tc := range toolCalls {
            streamBuf.AddAction(loopdetection.Action{
                Type:   tc.Type,   // e.g., "read", "grep"
                Target: tc.Target, // e.g., "config.go"
            })
        }
    }
    
    if streamBuf.ShouldAnalyze(false) { ... }
}
```

---

### Issue 4: Detector Per-Stream Instead of Per-Request (Medium Priority)

**File**: `pkg/proxy/handler_functions.go:337-339`

```go
// Initialize loop detection (shadow mode by default)
loopCfg := loopdetection.DefaultConfig()
detector := loopdetection.NewDetector(loopCfg)  // Created per-stream
streamBuf := detector.NewStreamBuffer()
```

**Problem**: For multi-turn conversations (retries, fallbacks), each SSE stream gets a fresh detector. Context is lost between attempts.

**Plan Requirement** (from loop-detection-plan.md):
> "To catch an agent stuck in a 'Read file → Fail → Read file → Fail' loop, the `MessageContext` window must span multiple turns of a single user request."

**Fix**: Store detector in `requestContext`:
```go
type requestContext struct {
    // ... existing fields
    loopDetector *loopdetection.Detector
}

// Initialize once per request, not per stream
func (h *Handler) handleStreamingResponse(...) {
    detector := rc.loopDetector  // Reuse across retries
    if detector == nil {
        rc.loopDetector = loopdetection.NewDetector(loopdetection.DefaultConfig())
        detector = rc.loopDetector
    }
    // ...
}
```

---

## 🟡 Missing from Plan

| Feature | Plan Phase | Implementation Status | Priority |
|---------|------------|----------------------|----------|
| Context window sanitization | Phase 3 | ❌ Not implemented | High |
| Tool call extraction | Phase 2 | ❌ Stub only | High |
| Reasoning model handling | Phase 3 | ❌ Not implemented | Medium |
| Cycle detection (graph) | Phase 3 | ❌ Not implemented | Low |
| Progress stagnation | Phase 3 | ❌ Not implemented | Low |
| `recovery/sanitize.go` | Phase 3 | ❌ Directory doesn't exist | Medium |

---

## 🟢 Minor Suggestions

### 1. Custom `itoa` Function (detector.go:152-173)
```go
// Current: Custom implementation to avoid strconv import
func itoa(i int) string { ... }

// Suggestion: Just use strconv.Itoa
import "strconv"
return "msg-" + strconv.Itoa(counter)
```
The import overhead is negligible. Standard library is clearer.

### 2. Duplicate Window Limiting (strategy_similarity.go:46-48)
```go
// The detector already limits window size, this is redundant
if len(eligible) > s.windowSize {
    eligible = eligible[len(eligible)-s.windowSize:]
}
```
Not a bug, just slight redundancy.

### 3. Hardcoded Sentence Threshold (buffer.go:57)
```go
if isCompleteSentence(b.textBuffer.String()) && b.tokenCount >= 5 {
    return true
}
```
The `>= 5` could be configurable via `Config`.

---

## Test Gaps

### Missing Test Cases
1. **Multi-turn loop detection** - Simulate retry/fallback scenario
2. **Tool call extraction** - End-to-end with actual SSE stream
3. **Concurrent analysis** - Race conditions with goroutines
4. **Memory bounds** - Verify sliding window doesn't grow unbounded

### Recommended Addition
```go
func TestDetector_MultiTurnPersistence(t *testing.T) {
    // Simulate: stream 1 detects nothing, stream 2 should see stream 1's context
    cfg := loopdetection.DefaultConfig()
    detector := loopdetection.NewDetector(cfg)
    
    // First "turn"
    detector.Analyze("Let me check config.go", []Action{{Type: "read", Target: "config.go"}})
    
    // Simulate Reset between streams (current behavior)
    detector.Reset()  
    
    // Second "turn" - would NOT detect loop because context was lost
    result := detector.Analyze("Let me check config.go", []Action{{Type: "read", Target: "config.go"}})
    
    // This demonstrates the per-stream issue
}
```

---

## Action Items

### Must Fix Before Production
1. [ ] Fix `AnalyzeActions` strategy reuse (2-line fix)
2. [ ] Wire up tool call extraction to `streamBuf.AddAction()`
3. [ ] Add integration test for action pattern detection

### Should Fix Soon
4. [ ] Move detector to per-request context for multi-turn persistence
5. [ ] Either use or remove `TrigramRepetitionRatio`

### Phase 3 / Future
6. [ ] Implement context window sanitization (`recovery/sanitize.go`)
7. [ ] Add reasoning model handling (lower thresholds for o1, o3)
8. [ ] Implement cycle detection in graphs

---

## Verdict

**Ready for Phase 1 shadow mode deployment** after fixing Issues #1 and #3.

The core detection logic is sound. The main gaps are integration details (tool call extraction) and multi-turn context persistence. These are fixable with small targeted changes.

**Confidence Level**: 85% for shadow mode, 60% for active interruption mode.
