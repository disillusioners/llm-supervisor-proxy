# Loop Detection - Implementation Complete

**Status**: ✅ Phase 4 Complete  
**Tests**: 33/33 passing  
**Build**: ✅ Passing

---

## Overview

The loop detection system monitors LLM responses for repetitive patterns and can intervene when the model enters a hallucination loop. It uses heuristic-based detection strategies (no additional LLM required) with configurable thresholds and shadow mode for safe rollout.

---

## Detection Strategies

### Phase 1 Strategies (Core)

| Strategy | Detects | File |
|----------|---------|------|
| **Exact Match** | Identical consecutive messages | `strategy_exact.go` |
| **Similarity** | Near-identical messages via SimHash | `strategy_similarity.go` |
| **Action Pattern** | Repeated tool/file operations | `strategy_actions.go` |

### Phase 3 Strategies (Advanced)

| Strategy | Detects | File |
|----------|---------|------|
| **Thinking** | Repetitive reasoning patterns via trigram analysis | `strategy_thinking.go` |
| **Cycle** | Circular action patterns (A→B→C→A→B→C) | `strategy_cycle.go` |
| **Stagnation** | No meaningful progress despite activity | `strategy_stagnation.go` |

---

## Strategy Details

### 1. Exact Match (`strategy_exact.go`)

**Detects**: Identical consecutive messages within sliding window

```go
// Triggered when same message appears N times consecutively (default: 3)
// Compares both ContentHash (SimHash) and raw Content
```

**Configuration**:
- `exact_match_count`: 3 (default)

**Example detection**:
```
Evidence: "Identical message repeated 3 times: 'Let me check that file...'"
Severity: Critical
Confidence: 1.0
```

---

### 2. Similarity (`strategy_similarity.go`)

**Detects**: Near-identical messages with minor variations

```go
// Uses SimHash to compare message fingerprints
// Only applies to messages with >= MinTokensForSimHash tokens
// Lower Hamming distance = higher similarity
```

**Configuration**:
- `similarity_threshold`: 0.85 (85% similar = potential loop)
- `min_tokens_for_simhash`: 15 (avoid false positives on short text)

**Example detection**:
```
Evidence: "Found 3 similar messages (threshold: 85%) in the last 3 messages"
Severity: Warning (2 matches) / Critical (3+ matches)
Confidence: 0.60 - 0.85
```

---

### 3. Action Pattern (`strategy_actions.go`)

**Detects**: Two patterns:
1. **Consecutive repeats**: Same action repeated (e.g., `read:config.go` x3)
2. **Oscillation**: A↔B pattern (e.g., `read` → `write` → `read` → `write`)

**Configuration**:
- `action_repeat_count`: 3 (consecutive identical actions)
- `oscillation_count`: 4 (A↔B oscillations)

**Example detection**:
```
Evidence: "Action 'read:config.go' repeated 3 times consecutively"
Evidence: "Oscillation detected: read:config.go ↔ write:config.go repeated 3 times"
Severity: Critical (repeats) / Warning (oscillation)
Confidence: 0.80 - 0.95
```

---

### 4. Thinking (`strategy_thinking.go`) - Phase 3

**Detects**: Repetitive reasoning/thinking patterns using trigram analysis

```go
// Accumulates thinking content across messages
// Calculates trigram repetition ratio: unique_trigrams / total_trigrams
// Lower ratio = more repetition = potential loop
// Special handling for reasoning models (o1, o3, deepseek-r1)
```

**Configuration**:
- `trigram_threshold`: 0.3 (ratio below this = loop)
- `thinking_min_tokens`: 100 (minimum before analysis)
- `reasoning_model_patterns`: ["o1", "o3", "deepseek-r1"]
- `reasoning_trigram_threshold`: 0.15 (more forgiving for reasoning models)

**Example detection**:
```
Evidence: "Thinking content has high trigram repetition (ratio=0.15, threshold=0.30, tokens=250)"
Severity: Warning / Critical (if very low ratio)
Confidence: 0.70 - 0.90
```

---

### 5. Cycle (`strategy_cycle.go`) - Phase 3

**Detects**: Circular patterns in multi-step action workflows

```go
// Looks for repeating action subsequences of length 2-5
// Catches patterns like A→B→C→A→B→C that ActionPattern misses
// Validates it's a genuine cycle (not all same action)
```

**Configuration**:
- `max_cycle_length`: 5 (maximum cycle length to check)
- `min_occurrences`: 2 (minimum repetitions to trigger)

**Example detection**:
```
Evidence: "Action cycle [read:config.go → grep:pattern → read:handler.go] repeated 2 times (cycle length: 3)"
Severity: Warning (2 occurrences) / Critical (3+ occurrences)
Confidence: 0.75 - 0.90
```

---

### 6. Stagnation (`strategy_stagnation.go`) - Phase 3

**Detects**: Messages being produced but no meaningful progress made

```go
// Compares per-message SimHash fingerprints across the window
// Computes average similarity of latest message vs all earlier ones
// Different from Similarity: detects stagnation across many messages, not just pairwise matches
```

**Configuration**:
- `window_size`: 5 (messages to consider)
- `change_threshold`: 0.3 (minimum 30% change required)
- `min_messages`: 5 (minimum before check)

**Example detection**:
```
Evidence: "Content stagnation detected: 86.7% average similarity across 5 messages (max acceptable: 70%)"
Severity: Warning / Critical (if very high similarity)
Confidence: 0.60 - 0.85
```

---

## Context Window Sanitization

**File**: `sanitize.go`

**Purpose**: Remove repetitive messages from conversation history to break loop patterns during recovery.

**Why it's critical**: LLMs will repeat mistakes if the context window contains many repetitions. Simply injecting a warning doesn't work.

### How It Works

```go
func SanitizeLoopHistory(messages []map[string]interface{}, result *DetectionResult) []map[string]interface{}
```

1. Calculates how many messages to trim based on `RepeatCount`
2. Keeps messages before the loop started
3. Adds a strategy-specific summary message

### Strategy-Specific Summaries

| Strategy | Summary Template |
|----------|-----------------|
| `exact_match` | "Your previous N responses were identical. Please take a completely different approach." |
| `similarity` | "Your previous N responses were nearly identical (X% similar). Please try a fundamentally different approach." |
| `action_pattern` | "Action 'X' was repeated N times without progress. Please try a different action or target." |
| `cycle` | "Action cycle detected (N repetitions). Please break the cycle by trying something new." |
| `thinking` | "Your reasoning has become repetitive. Please stop re-evaluating the same points and move forward." |
| `stagnation` | "No meaningful progress in the last N messages. Please summarize what you know and take concrete action." |

---

## Configuration

### Backend Config (`pkg/config/config.go`)

```go
type LoopDetectionConfig struct {
    Enabled              bool     `json:"enabled"`
    ShadowMode           bool     `json:"shadow_mode"`              // true = log only
    MessageWindow        int      `json:"message_window"`           // 10
    ActionWindow         int      `json:"action_window"`            // 15
    ExactMatchCount      int      `json:"exact_match_count"`        // 3
    SimilarityThreshold  float64  `json:"similarity_threshold"`     // 0.85
    MinTokensForSimHash  int      `json:"min_tokens_for_simhash"`   // 15
    ActionRepeatCount    int      `json:"action_repeat_count"`      // 3
    OscillationCount     int      `json:"oscillation_count"`        // 4
    MinTokensForAnalysis int      `json:"min_tokens_for_analysis"`  // 20
    
    // Phase 3 additions
    ThinkingMinTokens         int      `json:"thinking_min_tokens"`          // 100
    TrigramThreshold          float64  `json:"trigram_threshold"`            // 0.3
    MaxCycleLength            int      `json:"max_cycle_length"`             // 5
    ReasoningModelPatterns    []string `json:"reasoning_model_patterns"`     // ["o1", "o3", "deepseek-r1"]
    ReasoningTrigramThreshold float64  `json:"reasoning_trigram_threshold"`  // 0.15
}
```

### Default Values

| Setting | Default | Purpose |
|---------|---------|---------|
| `enabled` | `true` | Master toggle |
| `shadow_mode` | `true` | Log only, no interruption |
| `exact_match_count` | `3` | Identical messages to trigger |
| `similarity_threshold` | `0.85` | 85% similarity threshold |
| `trigram_threshold` | `0.30` | Repetition ratio threshold |
| `reasoning_trigram_threshold` | `0.15` | More forgiving for o1/o3 |

---

## Frontend Integration

### Configuration Tab

**File**: `pkg/ui/frontend/src/components/ConfigModal.tsx`

New "Loop Detection" tab with:
- ✅ Enable / Shadow Mode toggles
- ✅ Window size settings
- ✅ Exact match threshold
- ✅ Similarity detection settings
- ✅ Action pattern thresholds
- ✅ Stream processing settings

### Event Log Display

**File**: `pkg/ui/frontend/src/components/EventLog.tsx`

Loop detection events are displayed in real-time event log:

**Event Types**:
- `loop_detected` - Loop found (amber text)
- `loop_interrupted` - Stream stopped, retrying (red text)

**Event Messages**:
```typescript
case 'loop_detected': {
  const mode = d?.shadow_mode ? ' [shadow]' : '';
  return `Loop detected${mode}: ${d?.strategy} (${d?.severity}) — ${d?.evidence}`;
}
case 'loop_interrupted': {
  return `⚡ Loop interrupted: ${d?.strategy} — Stream stopped, retrying with sanitized context`;
}
```

**Color Coding**:
| Event | Color | Purpose |
|-------|-------|---------|
| `loop_detected` | Amber (`text-amber-400`) | Warning, monitoring |
| `loop_interrupted` | Red (`text-red-300`) | Active intervention |

**Type Definitions** (`types.ts`):
```typescript
export type EventType =
  | 'request_started'
  | 'request_completed'
  | 'loop_detected'      // Phase 4
  | 'loop_interrupted'   // Phase 4
  | 'retry_attempt'
  | 'timeout_idle'
  | 'error';
```

---

## File Structure

```
pkg/loopdetection/
├── detector.go              # Main orchestrator
├── config.go                # Configuration struct
├── types.go                 # Core types and interfaces
├── buffer.go                # Stream buffering logic
├── sanitize.go              # Context window sanitization (Phase 3)
├── strategy_exact.go        # Exact match detection
├── strategy_similarity.go   # SimHash similarity detection
├── strategy_actions.go      # Action pattern detection
├── strategy_thinking.go     # Thinking loop detection (Phase 3)
├── strategy_cycle.go        # Cycle detection (Phase 3)
├── strategy_stagnation.go   # Stagnation detection (Phase 3)
├── detector_test.go         # Test suite (33 tests)
└── fingerprint/
    ├── simhash.go           # SimHash implementation
    └── ngram.go             # Trigram analysis
```

---

## Test Coverage

| Category | Tests |
|----------|-------|
| SimHash / Fingerprint | 4 |
| Trigram | 3 |
| Exact Match | 2 |
| Similarity | 1 |
| Action Pattern | 2 |
| Thinking Strategy | 4 |
| Cycle Strategy | 3 |
| Stagnation Strategy | 2 |
| Sanitization | 3 |
| Stream Buffer | 3 |
| Configuration | 4 |
| Tokenizer | 2 |
| **Total** | **33** |

---

## Integration Points

### Stream Processing (`handler_functions.go`)

```go
// Per-request detector (persists across retries)
if rc.loopDetector == nil {
    rc.loopDetector = loopdetection.NewDetector(ldCfg)
}

// Stream buffering
streamBuf.AddText(newContent)

// Tool call extraction
if toolActions := extractToolCallActions(data); len(toolActions) > 0 {
    for _, action := range toolActions {
        streamBuf.AddAction(action)
    }
}

// Analysis when threshold met
if streamBuf.ShouldAnalyze(false) {
    if result := detector.Analyze(text, actions); result != nil {
        h.publishLoopEvent(rc.reqID, result, detector.IsShadowMode())
    }
}
```

### Event Publishing

```go
// Events published to event bus using typed LoopDetectionEvent struct
h.publishEvent("loop_detected", events.LoopDetectionEvent{
    RequestID:   reqID,
    Strategy:    result.Strategy,
    Severity:    result.Severity.String(),
    Evidence:    result.Evidence,
    Confidence:  result.Confidence,
    Pattern:     result.Pattern,
    RepeatCount: result.RepeatCount,
    ShadowMode:  shadowMode,
})
```

---

## Recovery Flow

When loop detected and `shadow_mode = false`:

### Flow Diagram

```
┌─────────────────┐
│ Loop Detected   │
│ (Critical)      │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Stop Stream     │
│ monitor.Close() │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Publish Event   │
│ loop_interrupted│
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Sanitize        │
│ Context Window  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Increment Retry │
│ Counter         │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Retry Request   │
│ (same model)    │
└─────────────────┘
```

### Implementation (`handler_functions.go`)

```go
// Phase 4: Hard interruption (when NOT in shadow mode)
if !detector.IsShadowMode() && result.Severity == loopdetection.SeverityCritical {
    log.Printf("[LOOP-DETECTION][INTERRUPT] Stopping stream — %s: %s", 
        result.Strategy, result.Evidence)
    
    h.publishEvent("loop_interrupted", events.LoopDetectionEvent{...})
    
    // Sanitize context window and trigger retry
    h.sanitizeAndRetry(rc, result)
    monitor.Close()
    counters.errorRetries++
    return attemptContinueRetry
}
```

### sanitizeAndRetry Implementation

```go
func (h *Handler) sanitizeAndRetry(rc *requestContext, result *loopdetection.DetectionResult) {
    // 1. Get current messages
    messages := rc.requestBody["messages"].([]interface{})
    
    // 2. Convert to format for sanitization
    msgMaps := convertMessages(messages)
    
    // 3. Append partial response as assistant message
    if rc.accumulatedResponse.Len() > 0 {
        msgMaps = append(msgMaps, map[string]interface{}{
            "role":    "assistant",
            "content": rc.accumulatedResponse.String(),
        })
    }
    
    // 4. Sanitize (trim loops, add summary)
    sanitized := loopdetection.SanitizeLoopHistory(msgMaps, result)
    
    // 5. Update request body for retry
    rc.requestBody["messages"] = sanitized
    
    // 6. Reset accumulated response
    rc.accumulatedResponse.Reset()
    rc.accumulatedThinking.Reset()
    
    log.Printf("[LOOP-DETECTION] Context sanitized: %d → %d messages", 
        len(messages), len(sanitized))
}
```

---

## Performance

- **SimHash**: O(n) where n = tokens
- **Sliding windows**: Slice append + trim, bounded to `MessageWindow` size
- **Cycle detection**: Bounded to recent actions within window
- **Target overhead**: <5ms per message, <10MB memory per request

---

## Phase Completion Summary

| Phase | Features | Status |
|-------|----------|--------|
| Phase 1 | Core detection (exact, similarity, actions) | ✅ Complete |
| Phase 2 | Integration, config, tool extraction | ✅ Complete |
| Phase 3 | Thinking, cycle, stagnation, sanitization | ✅ Complete |
| Phase 4 | Active interruption, recovery, UI events | ✅ Complete |

---

## Phase 4 Implementation Details

### What Was Implemented

| Feature | File | Status |
|---------|------|--------|
| **Active Interruption** | `handler_functions.go` (handleStreamResponse) | ✅ Complete |
| **sanitizeAndRetry** | `handler_functions.go` | ✅ Complete |
| **UI Event Display** | `EventLog.tsx` | ✅ Complete |
| **Event Types** | `types.ts` | ✅ Complete |
| **Event Publishing** | `handler_functions.go` (publishLoopEvent) | ✅ Complete |

### What's Still Missing

| Feature | Priority | Notes |
|---------|----------|-------|
| **Phase 4 Integration Tests** | Medium | No integration tests for hard interruption flow |
| **Loop Visualization** | Low | Request detail view could show loop patterns |

> **Note**: Model fallback on loop IS implemented — `sanitizeAndRetry` increments `errorRetries`, and when retry limits are exceeded, the existing fallback chain (via `attemptBreakToFallback`) kicks in automatically.

### Interruption Behavior

The system only interrupts when:
1. `shadow_mode = false` (configurable)
2. `severity = critical` (not just warnings)
3. Loop detected in streaming response

**Critical severity triggers**:
- Exact match: `ExactMatchCount`+ identical messages (default: 3)
- Similarity: 3+ similar messages (similarCount >= 2, i.e. latest + 2 matches)
- Action pattern: `ActionRepeatCount`+ consecutive identical actions (default: 3)
- Cycle: 3+ cycle repetitions (occurrences >= 3)
- Thinking: Very low trigram ratio (< threshold × 0.5, default: < 0.15)
- Stagnation: Average similarity > maxAcceptable + 15% (default: > 85%)

---

## Recommendations for Production

### Immediate Actions

1. **Start in Shadow Mode**: Default `shadow_mode: true` - collect data before enabling interruption
2. **Monitor Logs**: Review `[LOOP-DETECTION][SHADOW]` logs for tuning
3. **Tune Thresholds**: Adjust based on false positive/negative rates

### Before Enabling Active Interruption

4. **Test Thoroughly**: Run with realistic workloads in shadow mode first
5. **Set Thresholds Conservatively**: Higher thresholds = fewer false positives
6. **Monitor Recovery Success**: Track if sanitized retries actually work

### Future Enhancements

7. **Integration Tests**: Add Phase 4 tests for hard interruption and context sanitization flow
8. **Loop Analytics**: Dashboard showing loop patterns over time
9. **Per-Model Config**: Different thresholds for different models
10. **Max Loop Retries**: Add `max_loop_retries` config to limit sanitized retry attempts before fallback
